package main

import (
	"container/list"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/comerc/budva32/config"
	"github.com/comerc/budva32/utils"
	"github.com/dgraph-io/badger"
	"github.com/joho/godotenv"
	"github.com/zelenin/go-tdlib/client"
)

// TODO: а нужно ли собирать в БД сообщения, для которых не установлен ReplaceMyselfLinks (например "FTDAlgo-TSLA")
// TODO: нужна какая-то нотификация для каждого подписчика на выбранные хештеги
// TODO: подменять кештеги на хештеги (к одному виду) - для удобства пользования
// TODO: подключить @secinsidertrades вместо @insiders_ru
// TODO: изучить каналы в @TagNewsSenderbot
// TODO: проверить работоспособность видео и аудио сообщений
// TODO: закрыть порт для хождения снаружи, оставить только localhost
// TODO: беда с альбомами, добавляю SourceLink ко всем сообщениям, если первое было пустое - то не видно текста.
// TODO: Пересылка голосований?
// TODO: прикреплять myself link только к первому сообщению альбома (сейчас прикрепляется к любому, где была подпись)
// TODO: копирование отредактированных сообщений выполняется ответами на исходное (но ответы работают только группах, но не в каналах) - видно историю (Forward.EditHistory); но что делать с ReplaceMyselfLinks?
// TODO: почистить БД от "OptsFS-DST" - можно просто удалить все сообщения в канале
// TODO: log.Fatalf() внутри go func() не вызывает выход из программы
// TODO: setNewMessageId() + setTmpMessageId() & deleteNewMessageId() + deleteTmpMessageId() - в одну транзакцию
// TODO: для перевалочных каналов, куда выполняется системный forward (Copy2TSLA и ShuntTo) - не нужен setCopiedMessageId() && setNewMessageId() && setTmpMessageId();
// TODO: заменить на fmt.Errorf()
// TODO: убрать ContentMode; но нужно избавляться от formattedText в пользу Message.Content
// TODO: @usa100cks #Статистика
// TODO: ротация .log
// TODO: фильтровать выборку @stoxxxxx
// TODO: tradews - самые быстрые отчёты?
// TODO: флаг в конфиге для запрета пересылки документов
// TODO: для Exclude не только текст проверять, но и entities со ссылкой
// TODO: не умеет копировать голосования
// TODO: если не удалось обработать какое-либо сообщение, то отправлять его в канал Forward.Error
// TODO: Вырезать подпись (конфигурируемое) - беда с GetMarkdownText()
// TODO: синхронизировать закреп сообщений
// TODO: при копировании теряется картинка (заменяется на предпросмотр ссылки - из-за пробела для ссылки) https://t.me/Full_Time_Trading/46292
// TODO: если клиент был в офлайне, то каким образом он получает пропущенные сообщения? GetChatHistory() (хотя бот-API досылает пропущенные)
// TODO: если на момент начала пересылки не было исходного сообщения, то его редактирование не работает и ссылки на это сообщение ведут в никуда; надо создать вручную с мапингом на id исходного сообщения
// TODO: вырезать из сообщения ссылки по шаблону (https://t.me/c/1234/* - см. BRAVO)
// TODO: добавить справочник с константами для конфига
// TODO: вынести waitForForward в конфиг (не для всех каналов требуется ожидание реакции бота)
// TODO: Переводить https://t.me/pantini_cnbc или https://www.cnbc.com/rss-feeds/ или https://blog.feedspot.com/stock_rss_feeds/ или https://newsfilter.io/latest/news или https://www.businesswire.com/ или https://www.prnewswire.com/ через Google Translate API и копировать в @teslaholics
// TODO: ОГРОМНОЕ ТОРНАДО ПРОШЛО В ВЕРНОНЕ - похерился американский флаг при копировании на мобильной версии
// TODO: как бороться с зацикливанием пересылки
// TODO: фильтры, как исполняемые скрипты на node.js
// TODO: ротация лога
// TODO: падает при удалении целевого чата?
// TODO: Restart Go program by itself:
// https://github.com/rcrowley/goagain
// https://github.com/jpillora/overseer

const (
	projectName = "budva32"
)

var (
	inputCh  = make(chan string, 1)
	outputCh = make(chan string, 1)
	//
	uniqueFrom map[int64]struct{}
	//
	configData    *config.Config
	tdlibClient   *client.Client
	mediaAlbumsMu sync.Mutex
	// configMu      sync.Mutex
	badgerDB *badger.DB
)

func main() {
	log.SetFlags(log.LUTC | log.Ldate | log.Ltime | log.Lshortfile)
	var err error

	if err = godotenv.Load(); err != nil {
		log.Fatalf("Error loading .env file")
	}

	path := filepath.Join(".", ".tdata")
	if _, err = os.Stat(path); os.IsNotExist(err) {
		os.Mkdir(path, os.ModePerm)
	}

	{
		path := filepath.Join(path, "badger")
		if _, err = os.Stat(path); os.IsNotExist(err) {
			os.Mkdir(path, os.ModePerm)
		}
		badgerDB, err = badger.Open(badger.DefaultOptions(path))
		if err != nil {
			log.Fatal(err)
		}
	}
	defer badgerDB.Close()

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
		again:
			err := badgerDB.RunValueLogGC(0.7)
			if err == nil {
				goto again
			}
		}
	}()

	var (
		apiId       = os.Getenv("BUDVA32_API_ID")
		apiHash     = os.Getenv("BUDVA32_API_HASH")
		phonenumber = os.Getenv("BUDVA32_PHONENUMBER")
		port        = os.Getenv("BUDVA32_PORT")
	)

	go config.Watch(func() {
		tmpConfigData, err := config.Load()
		if err != nil {
			log.Fatalf("Can't initialise config: %s", err)
			return
		}
		for _, v := range tmpConfigData.ReplaceFragments {
			for from, to := range v {
				if utils.StrLen(from) != utils.StrLen(to) {
					err := fmt.Errorf(`strLen("%s") != strLen("%s")`, from, to)
					log.Print(err)
					return
				}
			}
		}
		tmpUniqueFrom := make(map[int64]struct{})
		re := regexp.MustCompile("[:,]")
		for forwardKey, forward := range tmpConfigData.Forwards {
			if re.FindString(forwardKey) != "" {
				err := fmt.Errorf("cannot use [:,] as Config.Forwards key in %s", forwardKey)
				log.Print(err)
				return
			}
			// TODO: "destination Id cannot be equal to source Id" - для всех From-To,
			// а не только для одного Forward; для будущей обработки To в UpdateDeleteMessages
			for _, dscChatId := range forward.To {
				if forward.From == dscChatId {
					err := fmt.Errorf("destination Id cannot be equal to source Id %d", dscChatId)
					log.Print(err)
					return
				}
			}
			tmpUniqueFrom[forward.From] = struct{}{}
		}
		// configMu.Lock()
		// defer configMu.Unlock()
		uniqueFrom = tmpUniqueFrom
		configData = tmpConfigData
	})

	go func() {
		http.HandleFunc("/favicon.ico", getFaviconHandler)
		http.HandleFunc("/", withBasicAuth(withAuthentiation(getChatsHandler)))
		http.HandleFunc("/ping", getPingHandler)
		http.HandleFunc("/answer", getAnswerHandler)
		host := getIP()
		port := ":" + port
		fmt.Println("Web-server is running: http://" + host + port)
		if err := http.ListenAndServe(port, http.DefaultServeMux); err != nil {
			log.Fatal("Error starting http server: ", err)
			return
		}
	}()

	// client authorizer
	authorizer := client.ClientAuthorizer()
	go func() {
		for {
			state, ok := <-authorizer.State
			if !ok {
				return
			}
			switch state.AuthorizationStateType() {
			case client.TypeAuthorizationStateWaitPhoneNumber:
				authorizer.PhoneNumber <- phonenumber
			case client.TypeAuthorizationStateWaitCode:
				outputCh <- fmt.Sprintf("Enter code for %s: ", phonenumber)
				code := <-inputCh
				authorizer.Code <- code
			case client.TypeAuthorizationStateWaitPassword:
				outputCh <- fmt.Sprintf("Enter password for %s: ", phonenumber)
				password := <-inputCh
				authorizer.Password <- password
			case client.TypeAuthorizationStateReady:
				return
			}
		}
	}()

	// or bot authorizer
	// botToken := "000000000:gsVCGG5YbikxYHC7bP5vRvmBqJ7Xz6vG6td"
	// authorizer := client.BotAuthorizer(botToken)

	authorizer.TdlibParameters <- &client.TdlibParameters{
		UseTestDc:              false,
		DatabaseDirectory:      filepath.Join(path, "db"),
		FilesDirectory:         filepath.Join(path, "files"),
		UseFileDatabase:        false,
		UseChatInfoDatabase:    false,
		UseMessageDatabase:     true,
		UseSecretChats:         false,
		ApiId:                  int32(convertToInt(apiId)),
		ApiHash:                apiHash,
		SystemLanguageCode:     "en",
		DeviceModel:            "Server",
		SystemVersion:          "1.0.0",
		ApplicationVersion:     "1.0.0",
		EnableStorageOptimizer: true,
		IgnoreFileNames:        false,
	}

	logStream := func(tdlibClient *client.Client) {
		tdlibClient.SetLogStream(&client.SetLogStreamRequest{
			LogStream: &client.LogStreamFile{
				Path:           filepath.Join(path, ".log"),
				MaxFileSize:    10485760,
				RedirectStderr: true,
			},
		})
	}

	logVerbosity := func(tdlibClient *client.Client) {
		tdlibClient.SetLogVerbosityLevel(&client.SetLogVerbosityLevelRequest{
			NewVerbosityLevel: 1,
		})
	}

	tdlibClient, err = client.NewClient(authorizer, logStream, logVerbosity)
	if err != nil {
		log.Fatalf("NewClient error: %s", err)
	}
	defer tdlibClient.Stop()

	outputCh <- "Ready!"

	log.Print("Start...")

	if optionValue, err := tdlibClient.GetOption(&client.GetOptionRequest{
		Name: "version",
	}); err != nil {
		log.Fatalf("GetOption error: %s", err)
	} else {
		log.Printf("TDLib version: %s", optionValue.(*client.OptionValueString).Value)
	}

	if me, err := tdlibClient.GetMe(); err != nil {
		log.Fatalf("GetMe error: %s", err)
	} else {
		log.Printf("Me: %s %s [@%s]", me.FirstName, me.LastName, me.Username)
	}

	listener := tdlibClient.GetListener()
	defer listener.Close()

	// Handle Ctrl+C
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		log.Print("Stop...")
		os.Exit(1)
	}()

	defer handlePanic()

	go runReports()

	go runQueue()

	for update := range listener.Updates {
		if update.GetClass() == client.ClassUpdate {
			switch updateType := update.(type) {
			case *client.UpdateNewMessage:
				updateNewMessage := updateType
				src := updateNewMessage.Message
				go func() {
					if _, ok := configData.DeleteSystemMessages[src.ChatId]; ok {
						needDelete := false
						switch src.Content.(type) {
						case *client.MessageChatChangeTitle:
							needDelete = true
						case *client.MessageChatChangePhoto:
							needDelete = true
						case *client.MessageChatDeletePhoto:
							needDelete = true
						case *client.MessageChatAddMembers:
							needDelete = true
						case *client.MessageChatDeleteMember:
							needDelete = true
						case *client.MessageChatJoinByLink:
							needDelete = true
						case *client.MessagePinMessage:
							needDelete = true
						}
						if needDelete {
							_, err := tdlibClient.DeleteMessages(&client.DeleteMessagesRequest{
								ChatId:     src.ChatId,
								MessageIds: []int64{src.Id},
								Revoke:     true,
							})
							if err != nil {
								log.Print(err)
							}
						}
					}
				}()
				if _, ok := uniqueFrom[src.ChatId]; !ok {
					continue
				}
				// TODO: так нельзя отключать, а почему?
				// if src.IsOutgoing {
				// 	log.Print("src.IsOutgoing > ", src.ChatId)
				// 	continue // !!
				// }
				if _, contentMode := getFormattedText(src.Content); contentMode == "" {
					continue
				}
				isExist := false
				checkFns := make(map[int64]func())
				otherFns := make(map[int64]func())
				forwardedTo := make(map[int64]bool)
				// var wg sync.WaitGroup
				// configData := getConfig()
				for forwardKey, forward := range configData.Forwards {
					// !!!! copy for go routine
					var (
						forwardKey = forwardKey
						forward    = forward
					)
					if src.ChatId == forward.From && (forward.SendCopy || src.CanBeForwarded) {
						isExist = true
						for _, dstChatId := range forward.To {
							_, isPresent := forwardedTo[dstChatId]
							if !isPresent {
								forwardedTo[dstChatId] = false
							}
						}
						if src.MediaAlbumId == 0 {
							// wg.Add(1)
							// log.Print("wg.Add > src.Id: ", src.Id)
							fn := func() {
								// defer func() {
								// 	wg.Done()
								// 	log.Print("wg.Done > src.Id: ", src.Id)
								// }()
								doUpdateNewMessage([]*client.Message{src}, forwardKey, forward, forwardedTo, checkFns, otherFns)
							}
							queue.PushBack(fn)
						} else {
							isFirstMessage := addMessageToMediaAlbum(forwardKey, src)
							if isFirstMessage {
								// wg.Add(1)
								// log.Print("wg.Add > src.Id: ", src.Id)
								fn := func() {
									handleMediaAlbum(forwardKey, src.MediaAlbumId,
										func(messages []*client.Message) {
											// defer func() {
											// 	wg.Done()
											// 	log.Print("wg.Done > src.Id: ", src.Id)
											// }()
											doUpdateNewMessage(messages, forwardKey, forward, forwardedTo, checkFns, otherFns)
										})
								}
								queue.PushBack(fn)
							}
						}
					}
				}
				if isExist {
					fn := func() {
						// wg.Wait()
						// log.Print("wg.Wait > src.Id: ", src.Id)
						for dstChatId, isForwarded := range forwardedTo {
							if isForwarded {
								incrementForwardedMessages(dstChatId)
							}
							incrementViewedMessages(dstChatId)
						}
						for check, fn := range checkFns {
							if fn == nil {
								log.Printf("check: %d is nil", check)
								continue
							}
							log.Printf("check: %d is fn()", check)
							fn()
						}
						for other, fn := range otherFns {
							if fn == nil {
								log.Printf("other: %d is nil", other)
								continue
							}
							log.Printf("other: %d is fn()", other)
							fn()
						}
					}
					queue.PushBack(fn)
				}
			case *client.UpdateMessageEdited:
				updateMessageEdited := updateType
				chatId := updateMessageEdited.ChatId
				if _, ok := uniqueFrom[chatId]; !ok {
					continue
				}
				messageId := updateMessageEdited.MessageId
				repeat := 0
				var fn func()
				fn = func() {
					var result []string
					fromChatMessageId := fmt.Sprintf("%d:%d", chatId, messageId)
					toChatMessageIds := getCopiedMessageIds(fromChatMessageId)
					log.Printf("UpdateMessageEdited > do > fromChatMessageId: %s toChatMessageIds: %v", fromChatMessageId, toChatMessageIds)
					defer func() {
						log.Printf("UpdateMessageEdited > ok > result: %v", result)
					}()
					if len(toChatMessageIds) == 0 {
						return
					}
					var newMessageIds = make(map[string]int64)
					isUpdateMessageSendSucceeded := true
					for _, toChatMessageId := range toChatMessageIds {
						a := strings.Split(toChatMessageId, ":")
						// forwardKey := a[0]
						dstChatId := int64(convertToInt(a[1]))
						tmpMessageId := int64(convertToInt(a[2]))
						newMessageId := getNewMessageId(dstChatId, tmpMessageId)
						if newMessageId == 0 {
							isUpdateMessageSendSucceeded = false
							break
						}
						newMessageIds[fmt.Sprintf("%d:%d", dstChatId, tmpMessageId)] = newMessageId
					}
					if !isUpdateMessageSendSucceeded {
						repeat++
						if repeat < 3 {
							log.Print("isUpdateMessageSendSucceeded > repeat: ", repeat)
							queue.PushBack(fn)
						} else {
							log.Print("isUpdateMessageSendSucceeded > repeat limit !!!")
						}
						return
					}
					src, err := tdlibClient.GetMessage(&client.GetMessageRequest{
						ChatId:    chatId,
						MessageId: messageId,
					})
					if err != nil {
						log.Print("GetMessage > ", err)
						return
					}
					// TODO: isAnswer
					_, hasReplyMarkupData := getReplyMarkupData(src)
					srcFormattedText, contentMode := getFormattedText(src.Content)
					log.Printf("srcChatId: %d srcId: %d hasText: %t MediaAlbumId: %d", src.ChatId, src.Id, srcFormattedText.Text != "", src.MediaAlbumId)
					checkFns := make(map[int64]func())
					for _, toChatMessageId := range toChatMessageIds {
						a := strings.Split(toChatMessageId, ":")
						forwardKey := a[0]
						dstChatId := int64(convertToInt(a[1]))
						tmpMessageId := int64(convertToInt(a[2]))
						formattedText := copyFormattedText(srcFormattedText)
						if forward, ok := configData.Forwards[forwardKey]; ok {
							if forward.CopyOnce {
								continue
							}
							if (forward.SendCopy || src.CanBeForwarded) && checkFilters(formattedText, forward) == FiltersCheck {
								_, ok := checkFns[forward.Check]
								if !ok {
									checkFns[forward.Check] = func() {
										const isSendCopy = false // обязательно надо форвардить, иначе невидно текущего сообщения
										forwardNewMessages(tdlibClient, []*client.Message{src}, src.ChatId, forward.Check, isSendCopy, forwardKey)
									}
								}
								continue
							}
						} else {
							continue
						}
						// hasFiltersCheck := false
						// testChatId := dstChatId
						// for _, forward := range configData.Forwards {
						// 	forward := forward // !!!! copy for go routine
						// 	if src.ChatId == forward.From && (forward.SendCopy || src.CanBeForwarded) {
						// 		for _, dstChatId := range forward.To {
						// 			if testChatId == dstChatId {
						// 				if checkFilters(formattedText, forward) == FiltersCheck {
						// 					hasFiltersCheck = true
						// 					_, ok := checkFns[forward.Check]
						// 					if !ok {
						// 						checkFns[forward.Check] = func() {
						// 							const isSendCopy = false // обязательно надо форвардить, иначе невидно текущего сообщения
						// 							forwardNewMessages(tdlibClient, []*client.Message{src}, src.ChatId, forward.Check, isSendCopy)
						// 						}
						// 					}
						// 				}
						// 			}
						// 		}
						// 	}
						// }
						// if hasFiltersCheck {
						// 	continue
						// }
						addAutoAnswer(formattedText, src)
						replaceMyselfLinks(formattedText, src.ChatId, dstChatId)
						replaceFragments(formattedText, dstChatId)
						// resetEntities(formattedText, dstChatId)
						addSources(formattedText, src, dstChatId)
						newMessageId := newMessageIds[fmt.Sprintf("%d:%d", dstChatId, tmpMessageId)]
						result = append(result, fmt.Sprintf("toChatMessageId: %s, newMessageId: %d", toChatMessageId, newMessageId))
						log.Print("contentMode: ", contentMode)
						switch contentMode {
						case ContentModeText:
							content := getInputMessageContent(src.Content, formattedText, contentMode)
							dst, err := tdlibClient.EditMessageText(&client.EditMessageTextRequest{
								ChatId:              dstChatId,
								MessageId:           newMessageId,
								InputMessageContent: content,
								// ReplyMarkup: src.ReplyMarkup, // это не надо, юзер-бот игнорит изменение
							})
							if err != nil {
								log.Print("EditMessageText > ", err)
							}
							log.Printf("EditMessageText > dst: %#v", dst)
						case ContentModeAnimation:
							fallthrough
						case ContentModeDocument:
							fallthrough
						case ContentModeAudio:
							fallthrough
						case ContentModeVideo:
							fallthrough
						case ContentModePhoto:
							content := getInputMessageContent(src.Content, formattedText, contentMode)
							dst, err := tdlibClient.EditMessageMedia(&client.EditMessageMediaRequest{
								ChatId:              dstChatId,
								MessageId:           newMessageId,
								InputMessageContent: content,
							})
							if err != nil {
								log.Print("EditMessageMedia > ", err)
							}
							log.Printf("EditMessageMedia > dst: %#v", dst)
						case ContentModeVoiceNote:
							dst, err := tdlibClient.EditMessageCaption(&client.EditMessageCaptionRequest{
								ChatId:    dstChatId,
								MessageId: newMessageId,
								Caption:   formattedText,
							})
							if err != nil {
								log.Print("EditMessageCaption > ", err)
							}
							log.Printf("EditMessageCaption > dst: %#v", dst)
						default:
							continue
						}
						// TODO: isAnswer
						if hasReplyMarkupData {
							setAnswerMessageId(dstChatId, tmpMessageId, fromChatMessageId)
						} else {
							deleteAnswerMessageId(dstChatId, tmpMessageId)
						}
					}
					for check, fn := range checkFns {
						if fn == nil {
							log.Printf("check: %d is nil", check)
							continue
						}
						log.Printf("check: %d is fn()", check)
						fn()
					}
				}
				queue.PushBack(fn)
			case *client.UpdateMessageSendSucceeded:
				updateMessageSendSucceeded := updateType
				message := updateMessageSendSucceeded.Message
				tmpMessageId := updateMessageSendSucceeded.OldMessageId
				fn := func() {
					log.Print("UpdateMessageSendSucceeded > go")
					setNewMessageId(message.ChatId, tmpMessageId, message.Id)
					setTmpMessageId(message.ChatId, message.Id, tmpMessageId)
					log.Print("UpdateMessageSendSucceeded > ok")
				}
				queue.PushBack(fn)
			case *client.UpdateDeleteMessages:
				updateDeleteMessages := updateType
				if !updateDeleteMessages.IsPermanent {
					continue
				}
				chatId := updateDeleteMessages.ChatId
				if _, ok := uniqueFrom[chatId]; !ok {
					continue
				}
				// TODO: а если удаление произошло в Forward.To - тоже надо чистить БД
				messageIds := updateDeleteMessages.MessageIds
				repeat := 0
				var fn func()
				fn = func() {
					var result []string
					log.Printf("UpdateDeleteMessages > do > chatId: %d messageIds: %v", chatId, messageIds)
					defer func() {
						log.Printf("UpdateDeleteMessages > ok > result: %v", result)
					}()
					var copiedMessageIds = make(map[string][]string)
					var newMessageIds = make(map[string]int64)
					isUpdateMessageSendSucceeded := true
					for _, messageId := range messageIds {
						fromChatMessageId := fmt.Sprintf("%d:%d", chatId, messageId)
						toChatMessageIds := getCopiedMessageIds(fromChatMessageId)
						copiedMessageIds[fromChatMessageId] = toChatMessageIds
						for _, toChatMessageId := range toChatMessageIds {
							a := strings.Split(toChatMessageId, ":")
							// forwardKey := a[0]
							dstChatId := int64(convertToInt(a[1]))
							tmpMessageId := int64(convertToInt(a[2]))
							newMessageId := getNewMessageId(dstChatId, tmpMessageId)
							if newMessageId == 0 {
								isUpdateMessageSendSucceeded = false
								break
							}
							newMessageIds[fmt.Sprintf("%d:%d", dstChatId, tmpMessageId)] = newMessageId
						}
					}
					if !isUpdateMessageSendSucceeded {
						repeat++
						if repeat < 3 {
							log.Print("isUpdateMessageSendSucceeded > repeat: ", repeat)
							queue.PushBack(fn)
						} else {
							log.Print("isUpdateMessageSendSucceeded > repeat limit !!!")
						}
						return
					}
					for _, messageId := range messageIds {
						fromChatMessageId := fmt.Sprintf("%d:%d", chatId, messageId)
						toChatMessageIds := copiedMessageIds[fromChatMessageId]
						for _, toChatMessageId := range toChatMessageIds {
							a := strings.Split(toChatMessageId, ":")
							forwardKey := a[0]
							dstChatId := int64(convertToInt(a[1]))
							tmpMessageId := int64(convertToInt(a[2]))
							if forward, ok := configData.Forwards[forwardKey]; ok {
								if forward.Indelible {
									continue
								}
							} else {
								continue
							}
							deleteAnswerMessageId(dstChatId, tmpMessageId)
							newMessageId := newMessageIds[fmt.Sprintf("%d:%d", dstChatId, tmpMessageId)]
							result = append(result, fmt.Sprintf("%d:%d:%d", dstChatId, tmpMessageId, newMessageId))
							deleteTmpMessageId(dstChatId, newMessageId)
							deleteNewMessageId(dstChatId, tmpMessageId)
							_, err := tdlibClient.DeleteMessages(&client.DeleteMessagesRequest{
								ChatId:     dstChatId,
								MessageIds: []int64{newMessageId},
								Revoke:     true,
							})
							if err != nil {
								log.Print("DeleteMessages > ", err)
								continue
							}
						}
						if len(toChatMessageIds) > 0 {
							deleteCopiedMessageIds(fromChatMessageId)
						}
					}
				}
				queue.PushBack(fn)
			}
		}
	}
}

func runReports() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for t := range ticker.C {
		utc := t.UTC()
		// w := utc.Weekday()
		// if w == 0 || w == 1 {
		// 	continue
		// }
		h := utc.Hour()
		m := utc.Minute()
		if h == 0 && m == 0 {
			// configData := getConfig()
			for _, toChatId := range configData.Reports.For {
				date := utc.Add(-1 * time.Minute).Format("2006-01-02")
				var viewed, forwarded int64
				{
					key := []byte(fmt.Sprintf("%s:%d:%s", viewedMessagesPrefix, toChatId, date))
					val := getForDB(key)
					if len(val) == 0 {
						viewed = 0
					} else {
						viewed = int64(bytesToUint64(val))
					}
				}
				{
					key := []byte(fmt.Sprintf("%s:%d:%s", forwardedMessagesPrefix, toChatId, date))
					val := getForDB(key)
					if len(val) == 0 {
						forwarded = 0
					} else {
						forwarded = int64(bytesToUint64(val))
					}
				}
				formattedText, err := tdlibClient.ParseTextEntities(&client.ParseTextEntitiesRequest{
					Text: fmt.Sprintf(configData.Reports.Template, forwarded, viewed),
					ParseMode: &client.TextParseModeMarkdown{
						Version: 2,
					},
				})
				if err != nil {
					log.Print("ParseTextEntities > ", err)
				} else {
					if _, err := tdlibClient.SendMessage(&client.SendMessageRequest{
						ChatId: toChatId,
						InputMessageContent: &client.InputMessageText{
							Text:                  formattedText,
							DisableWebPagePreview: true,
							ClearDraft:            true,
						},
						Options: &client.MessageSendOptions{
							DisableNotification: true,
						},
					}); err != nil {
						log.Print("SendMessage > ", err)
					}
				}
			}
		}
	}
}

func convertToInt(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		log.Print("convertToInt > ", err)
		return 0
	}
	return int(i)
}

// ****

// type FromChatMessageId string // copiedMessageIdsPrefix:srcChatId:srcMessageId
// type ToChatMessageId string // forwardKey:dstChatId:tmpMessageId // !! tmp

// var copiedMessageIds = make(map[FromChatMessageId][]ToChatMessageId)

const copiedMessageIdsPrefix = "copiedMsgIds"

func deleteCopiedMessageIds(fromChatMessageId string) {
	key := []byte(fmt.Sprintf("%s:%s", copiedMessageIdsPrefix, fromChatMessageId))
	err := badgerDB.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
	if err != nil {
		log.Print(err)
	}
	log.Printf("deleteCopiedMessageIds > fromChatMessageId: %s", fromChatMessageId)
}

func setCopiedMessageId(fromChatMessageId string, toChatMessageId string) {
	key := []byte(fmt.Sprintf("%s:%s", copiedMessageIdsPrefix, fromChatMessageId))
	var (
		err error
		val []byte
	)
	err = badgerDB.Update(func(txn *badger.Txn) error {
		var item *badger.Item
		item, err = txn.Get(key)
		if err != nil && err != badger.ErrKeyNotFound {
			return err
		}
		if err != badger.ErrKeyNotFound {
			val, err = item.ValueCopy(nil)
			if err != nil {
				return err
			}
		}
		result := []string{}
		s := fmt.Sprintf("%s", val)
		if s != "" {
			// workaround https://stackoverflow.com/questions/28330908/how-to-string-split-an-empty-string-in-go
			result = strings.Split(s, ",")
		}
		val = []byte(strings.Join(distinct(append(result, toChatMessageId)), ","))
		// val = []byte(strings.Join(toChatMessageIds, ","))
		return txn.Set(key, val)
	})
	if err != nil {
		log.Print("setCopiedMessageId > ", err)
	}
	log.Printf("setCopiedMessageId > fromChatMessageId: %s toChatMessageId: %s val: %s", fromChatMessageId, toChatMessageId, val)
}

func getCopiedMessageIds(fromChatMessageId string) []string {
	key := []byte(fmt.Sprintf("%s:%s", copiedMessageIdsPrefix, fromChatMessageId))
	var (
		err error
		val []byte
	)
	err = badgerDB.View(func(txn *badger.Txn) error {
		var item *badger.Item
		item, err = txn.Get(key)
		if err != nil {
			return err
		}
		if err != badger.ErrKeyNotFound {
			val, err = item.ValueCopy(nil)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Print("getCopiedMessageIds > ", err)
	}
	toChatMessageIds := []string{}
	s := fmt.Sprintf("%s", val)
	if s != "" {
		// workaround https://stackoverflow.com/questions/28330908/how-to-string-split-an-empty-string-in-go
		toChatMessageIds = strings.Split(s, ",")
	}
	log.Printf("getCopiedMessageIds > fromChatMessageId: %s toChatMessageIds: %v", fromChatMessageId, toChatMessageIds)
	return toChatMessageIds
}

// var newMessageIds = make(map[string]int64)

const newMessageIdPrefix = "newMsgId"

func setNewMessageId(chatId, tmpMessageId, newMessageId int64) {
	key := []byte(fmt.Sprintf("%s:%d:%d", newMessageIdPrefix, chatId, tmpMessageId))
	val := []byte(fmt.Sprintf("%d", newMessageId))
	err := badgerDB.Update(func(txn *badger.Txn) error {
		err := txn.Set(key, val)
		return err
	})
	if err != nil {
		log.Print("setNewMessageId > ", err)
	}
	log.Printf("setNewMessageId > key: %d:%d val: %d", chatId, tmpMessageId, newMessageId)
	// newMessageIds[ChatMessageId(fmt.Sprintf("%d:%d", chatId, tmpMessageId))] = newMessageId
}

func getNewMessageId(chatId, tmpMessageId int64) int64 {
	key := []byte(fmt.Sprintf("%s:%d:%d", newMessageIdPrefix, chatId, tmpMessageId))
	var (
		err error
		val []byte
	)
	err = badgerDB.View(func(txn *badger.Txn) error {
		var item *badger.Item
		item, err = txn.Get(key)
		if err != nil {
			return err
		}
		val, err = item.ValueCopy(nil)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Print("getNewMessageId > ", err)
		return 0
	}
	newMessageId := int64(convertToInt(fmt.Sprintf("%s", val)))
	log.Printf("getNewMessageId > key: %d:%d val: %d", chatId, tmpMessageId, newMessageId)
	return newMessageId
	// return newMessageIds[ChatMessageId(fmt.Sprintf("%d:%d", chatId, tmpMessageId))]
}

func deleteNewMessageId(chatId, tmpMessageId int64) {
	key := []byte(fmt.Sprintf("%s:%d:%d", newMessageIdPrefix, chatId, tmpMessageId))
	err := badgerDB.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
	if err != nil {
		log.Print(err)
	}
	log.Printf("deleteNewMessageId > key: %d:%d", chatId, tmpMessageId)
}

const tmpMessageIdPrefix = "tmpMsgId"

func setTmpMessageId(chatId, newMessageId, tmpMessageId int64) {
	key := []byte(fmt.Sprintf("%s:%d:%d", tmpMessageIdPrefix, chatId, newMessageId))
	val := []byte(fmt.Sprintf("%d", tmpMessageId))
	err := badgerDB.Update(func(txn *badger.Txn) error {
		err := txn.Set(key, val)
		return err
	})
	if err != nil {
		log.Print("setTmpMessageId > ", err)
	}
	log.Printf("setTmpMessageId > key: %d:%d val: %d", chatId, newMessageId, tmpMessageId)
}

func getTmpMessageId(chatId, newMessageId int64) int64 {
	key := []byte(fmt.Sprintf("%s:%d:%d", tmpMessageIdPrefix, chatId, newMessageId))
	var (
		err error
		val []byte
	)
	err = badgerDB.View(func(txn *badger.Txn) error {
		var item *badger.Item
		item, err = txn.Get(key)
		if err != nil {
			return err
		}
		val, err = item.ValueCopy(nil)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Print("getTmpMessageId > ", err)
		return 0
	}
	tmpMessageId := int64(convertToInt(fmt.Sprintf("%s", val)))
	log.Printf("getTmpMessageId > key: %d:%d val: %d", chatId, newMessageId, tmpMessageId)
	return tmpMessageId
}

func deleteTmpMessageId(chatId, newMessageId int64) {
	key := []byte(fmt.Sprintf("%s:%d:%d", tmpMessageIdPrefix, chatId, newMessageId))
	err := badgerDB.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
	if err != nil {
		log.Print(err)
	}
	log.Printf("deleteTmpMessageId > key: %d:%d", chatId, newMessageId)
}

var (
	lastForwarded   = make(map[int64]time.Time)
	lastForwardedMu sync.Mutex
)

func getLastForwardedDiff(chatId int64) time.Duration {
	lastForwardedMu.Lock()
	defer lastForwardedMu.Unlock()
	return time.Since(lastForwarded[chatId])
}

func setLastForwarded(chatId int64) {
	lastForwardedMu.Lock()
	defer lastForwardedMu.Unlock()
	lastForwarded[chatId] = time.Now()
}

func forwardNewMessages(tdlibClient *client.Client, messages []*client.Message, srcChatId, dstChatId int64, isSendCopy bool, forwardKey string) {
	log.Printf("forwardNewMessages > srcChatId: %d dstChatId: %d", srcChatId, dstChatId)
	diff := getLastForwardedDiff(dstChatId)
	if diff < waitForForward {
		time.Sleep(waitForForward - diff)
	}
	setLastForwarded(dstChatId)
	var (
		result *client.Messages
		err    error
	)
	if isSendCopy {
		contents := make([]client.InputMessageContent, 0)
		for i, message := range messages {
			if message.ForwardInfo != nil {
				if origin, ok := message.ForwardInfo.Origin.(*client.MessageForwardOriginChannel); ok {
					if originMessage, err := tdlibClient.GetMessage(&client.GetMessageRequest{
						ChatId:    origin.ChatId,
						MessageId: origin.MessageId,
					}); err != nil {
						log.Print("originMessage ", err)
					} else {
						targetMessage := message
						targetFormattedText, _ := getFormattedText(targetMessage.Content)
						originFormattedText, _ := getFormattedText(originMessage.Content)
						// workaround for https://github.com/tdlib/td/issues/1572
						if targetFormattedText.Text == originFormattedText.Text {
							messages[i] = originMessage
						} else {
							log.Print("targetMessage != originMessage")
						}
					}
				}
			}
			src := messages[i] // !!!! for origin message
			srcFormattedText, contentMode := getFormattedText(src.Content)
			formattedText := copyFormattedText(srcFormattedText)
			addAutoAnswer(formattedText, src)
			replaceMyselfLinks(formattedText, src.ChatId, dstChatId)
			replaceFragments(formattedText, dstChatId)
			// resetEntities(formattedText, dstChatId)
			if i == 0 {
				addSources(formattedText, src, dstChatId)
			}
			content := getInputMessageContent(src.Content, formattedText, contentMode)
			if content != nil {
				contents = append(contents, content)
			}
		}
		var replyToMessageId int64 = 0
		src := messages[0]
		if src.ReplyToMessageId > 0 && src.ReplyInChatId == src.ChatId {
			fromChatMessageId := fmt.Sprintf("%d:%d", src.ReplyInChatId, src.ReplyToMessageId)
			toChatMessageIds := getCopiedMessageIds(fromChatMessageId)
			var tmpMessageId int64 = 0
			for _, toChatMessageId := range toChatMessageIds {
				a := strings.Split(toChatMessageId, ":")
				if int64(convertToInt(a[1])) == dstChatId {
					tmpMessageId = int64(convertToInt(a[2]))
					break
				}
			}
			if tmpMessageId != 0 {
				replyToMessageId = getNewMessageId(dstChatId, tmpMessageId)
			}
		}
		if len(contents) == 1 {
			var message *client.Message
			message, err = tdlibClient.SendMessage(&client.SendMessageRequest{
				ChatId:              dstChatId,
				InputMessageContent: contents[0],
				ReplyToMessageId:    replyToMessageId,
			})
			if err != nil {
				// nothing
			} else {
				result = &client.Messages{
					TotalCount: 1,
					Messages:   []*client.Message{message},
				}
			}
		} else {
			result, err = tdlibClient.SendMessageAlbum(&client.SendMessageAlbumRequest{
				ChatId:               dstChatId,
				InputMessageContents: contents,
				ReplyToMessageId:     replyToMessageId,
			})
		}
	} else {
		result, err = tdlibClient.ForwardMessages(&client.ForwardMessagesRequest{
			ChatId:     dstChatId,
			FromChatId: srcChatId,
			MessageIds: func() []int64 {
				var messageIds []int64
				for _, message := range messages {
					messageIds = append(messageIds, message.Id)
				}
				return messageIds
			}(),
			Options: &client.MessageSendOptions{
				DisableNotification: false,
				FromBackground:      false,
				SchedulingState: &client.MessageSchedulingStateSendAtDate{
					SendDate: int32(time.Now().Unix()),
				},
			},
			SendCopy:      false,
			RemoveCaption: false,
		})
	}
	if err != nil {
		log.Print("forwardNewMessages > ", err)
	} else if len(result.Messages) != int(result.TotalCount) || result.TotalCount == 0 {
		log.Print("forwardNewMessages > invalid TotalCount")
	} else if len(result.Messages) != len(messages) {
		log.Print("forwardNewMessages > invalid len(messages)")
	} else if isSendCopy {
		for i, dst := range result.Messages {
			if dst == nil {
				log.Printf("!!!! dst == nil !!!! result: %#v messages: %#v", result, messages)
				continue
			}
			tmpMessageId := dst.Id
			src := messages[i] // !!!! for origin message
			toChatMessageId := fmt.Sprintf("%s:%d:%d", forwardKey, dstChatId, tmpMessageId)
			fromChatMessageId := fmt.Sprintf("%d:%d", src.ChatId, src.Id)
			setCopiedMessageId(fromChatMessageId, toChatMessageId)
			// TODO: isAnswer
			if _, ok := getReplyMarkupData(src); ok {
				setAnswerMessageId(dstChatId, tmpMessageId, fromChatMessageId)
			}
		}
	}
}

func getReplyMarkupData(message *client.Message) ([]byte, bool) {
	if message.ReplyMarkup != nil {
		if a, ok := message.ReplyMarkup.(*client.ReplyMarkupInlineKeyboard); ok {
			row := a.Rows[0]
			btn := row[0]
			if callback, ok := btn.Type.(*client.InlineKeyboardButtonTypeCallback); ok {
				return callback.Data, true
			}
		}
	}
	return nil, false
}

type ContentMode string

const (
	ContentModeText      = "text"
	ContentModeAnimation = "animation"
	ContentModeAudio     = "audio"
	ContentModeDocument  = "document"
	ContentModePhoto     = "photo"
	ContentModeVideo     = "video"
	ContentModeVoiceNote = "voiceNote"
)

func getFormattedText(messageContent client.MessageContent) (*client.FormattedText, ContentMode) {
	var (
		formattedText *client.FormattedText
		contentMode   ContentMode
	)
	// TODO: как использовать switch для разблюдовки по приведению типа?
	if content, ok := messageContent.(*client.MessageText); ok {
		formattedText = content.Text
		contentMode = ContentModeText
	} else if content, ok := messageContent.(*client.MessagePhoto); ok {
		formattedText = content.Caption
		contentMode = ContentModePhoto
	} else if content, ok := messageContent.(*client.MessageAnimation); ok {
		formattedText = content.Caption
		contentMode = ContentModeAnimation
	} else if content, ok := messageContent.(*client.MessageAudio); ok {
		formattedText = content.Caption
		contentMode = ContentModeAudio
	} else if content, ok := messageContent.(*client.MessageDocument); ok {
		formattedText = content.Caption
		contentMode = ContentModeDocument
	} else if content, ok := messageContent.(*client.MessageVideo); ok {
		formattedText = content.Caption
		contentMode = ContentModeVideo
	} else if content, ok := messageContent.(*client.MessageVoiceNote); ok {
		formattedText = content.Caption
		contentMode = ContentModeVoiceNote
	} else {
		// client.MessageExpiredPhoto
		// client.MessageSticker
		// client.MessageExpiredVideo
		// client.MessageVideoNote
		// client.MessageLocation
		// client.MessageVenue
		// client.MessageContact
		// client.MessageDice
		// client.MessageGame
		// client.MessagePoll
		// client.MessageInvoice
		formattedText = &client.FormattedText{Entities: make([]*client.TextEntity, 0)}
		contentMode = ""
	}
	return formattedText, contentMode
}

func contains(a []string, s string) bool {
	for _, t := range a {
		if t == s {
			return true
		}
	}
	return false
}

func containsInt64(a []int64, e int64) bool {
	for _, t := range a {
		if t == e {
			return true
		}
	}
	return false
}

type FiltersMode string

const (
	FiltersOK    FiltersMode = "ok"
	FiltersCheck FiltersMode = "check"
	FiltersOther FiltersMode = "other"
)

func checkFilters(formattedText *client.FormattedText, forward config.Forward) FiltersMode {
	if formattedText.Text == "" {
		hasInclude := false
		if forward.Include != "" {
			hasInclude = true
		}
		for _, includeSubmatch := range forward.IncludeSubmatch {
			if includeSubmatch.Regexp != "" {
				hasInclude = true
				break
			}
		}
		if hasInclude {
			return FiltersOther
		}
	} else {
		if forward.Exclude != "" {
			re := regexp.MustCompile("(?i)" + forward.Exclude)
			if re.FindString(formattedText.Text) != "" {
				return FiltersCheck
			}
		}
		hasInclude := false
		if forward.Include != "" {
			hasInclude = true
			re := regexp.MustCompile("(?i)" + forward.Include)
			if re.FindString(formattedText.Text) != "" {
				return FiltersOK
			}
		}
		for _, includeSubmatch := range forward.IncludeSubmatch {
			if includeSubmatch.Regexp != "" {
				hasInclude = true
				re := regexp.MustCompile("(?i)" + includeSubmatch.Regexp)
				matches := re.FindAllStringSubmatch(formattedText.Text, -1)
				for _, match := range matches {
					s := match[includeSubmatch.Group]
					if contains(includeSubmatch.Match, s) {
						return FiltersOK
					}
				}
			}
		}
		if hasInclude {
			return FiltersOther
		}
	}
	return FiltersOK
}

func withBasicAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), []byte(os.Getenv("BUDVA32_USER"))) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(os.Getenv("BUDVA32_PASS"))) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Please enter your username and password"`)
			http.Error(w, "You are unauthorized to access the application.\n", http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}
}

func withAuthentiation(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if outputCh != nil {
			if r.Method == "POST" {
				r.ParseForm()
				if len(r.PostForm["input"]) == 1 {
					input := r.PostForm["input"][0]
					inputCh <- input
				}
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			output := <-outputCh
			if output != "Ready!" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				io.WriteString(w, fmt.Sprintf(`<html><head><title>%s</title></head><body><form method="post">%s<input autocomplete="off" name="input" /><input type="submit" /></form></body></html>`, projectName, output))
				return
			}
			outputCh = nil
		}
		handler(w, r)
	}
}

func getIP() string {
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		addrs, _ := i.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			return ip.String()
		}
	}
	return ""
}

func getFaviconHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/favicon.ico")
}

func getAnswerHandler(w http.ResponseWriter, r *http.Request) {
	// использует коды ошибок HTTP для статусов: error, ok, wait
	// 200 OK
	// 204 No Content
	// 205 Reset Content
	// 500 Internal Server Error
	// TODO: накапливать статистику по параметру step, чтобы подкрутить паузу в shunt
	q := r.URL.Query()
	log.Printf("getAnswerHandler > %#v", q)
	var isOnlyCheck bool
	if len(q["only_check"]) == 1 {
		isOnlyCheck = q["only_check"][0] == "1"
	}
	var dstChatId int64
	if len(q["chat_id"]) == 1 {
		dstChatId = int64(convertToInt(q["chat_id"][0]))
	}
	var newMessageId int64
	if len(q["message_id"]) == 1 {
		newMessageId = int64(convertToInt(q["message_id"][0]))
	}
	if dstChatId == 0 || newMessageId == 0 {
		err := fmt.Errorf("invalid input parameters")
		log.Print(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpMessageId := getTmpMessageId(dstChatId, newMessageId)
	if tmpMessageId == 0 {
		log.Print("http.StatusNoContent")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	fromChatMessageId := getAnswerMessageId(dstChatId, tmpMessageId)
	if fromChatMessageId == "" {
		log.Print("http.StatusResetContent #1")
		w.WriteHeader(http.StatusResetContent)
		return
	}
	a := strings.Split(fromChatMessageId, ":")
	srcChatId := int64(convertToInt(a[0]))
	srcMessageId := int64(convertToInt(a[1]))
	message, err := tdlibClient.GetMessage(&client.GetMessageRequest{
		ChatId:    srcChatId,
		MessageId: srcMessageId,
	})
	if err != nil {
		log.Print(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, ok := getReplyMarkupData(message)
	if !ok {
		log.Print("http.StatusResetContent #2")
		w.WriteHeader(http.StatusResetContent)
		return
	}
	if !isOnlyCheck {
		answer, err := tdlibClient.GetCallbackQueryAnswer(&client.GetCallbackQueryAnswerRequest{
			ChatId:    srcChatId,
			MessageId: srcMessageId,
			Payload:   &client.CallbackQueryPayloadData{Data: data},
		})
		if err != nil {
			log.Print(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, answer.Text)
	}
}

func getPingHandler(w http.ResponseWriter, r *http.Request) {
	ret, err := time.Now().UTC().MarshalJSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, fmt.Sprintf("{now:%s}", string(ret)))
}

func getChatsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var limit = 1000
	if len(q["limit"]) == 1 {
		limit = convertToInt(q["limit"][0])
	}
	allChats, err := getChatList(tdlibClient, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	retMap := make(map[string]interface{})
	retMap["total"] = len(allChats)
	var chatList []string
	for _, chat := range allChats {
		chatList = append(chatList, fmt.Sprintf("%d=%s", chat.Id, chat.Title))
	}
	retMap["chatList"] = chatList
	ret, err := json.Marshal(retMap)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, string(ret))
}

// see https://stackoverflow.com/questions/37782348/how-to-use-getchats-in-tdlib
func getChatList(tdlibClient *client.Client, limit int) ([]*client.Chat, error) {
	var (
		allChats     []*client.Chat
		offsetOrder  = int64(math.MaxInt64)
		offsetChatId = int64(0)
	)
	for len(allChats) < limit {
		if len(allChats) > 0 {
			lastChat := allChats[len(allChats)-1]
			for i := 0; i < len(lastChat.Positions); i++ {
				if lastChat.Positions[i].List.ChatListType() == client.TypeChatListMain {
					offsetOrder = int64(lastChat.Positions[i].Order)
				}
			}
			offsetChatId = lastChat.Id
		}
		chats, err := tdlibClient.GetChats(&client.GetChatsRequest{
			ChatList:     &client.ChatListMain{},
			Limit:        int32(limit - len(allChats)),
			OffsetOrder:  client.JsonInt64(offsetOrder),
			OffsetChatId: offsetChatId,
		})
		if err != nil {
			return nil, err
		}
		if len(chats.ChatIds) == 0 {
			return allChats, nil
		}
		for _, chatId := range chats.ChatIds {
			chat, err := tdlibClient.GetChat(&client.GetChatRequest{
				ChatId: chatId,
			})
			if err == nil {
				allChats = append(allChats, chat)
			} else {
				return nil, err
			}
		}
	}
	return allChats, nil
}

type MediaAlbum struct {
	messages     []*client.Message
	lastReceived time.Time
}

var mediaAlbums = make(map[string]MediaAlbum)

// https://github.com/tdlib/td/issues/1482
func addMessageToMediaAlbum(forwardKey string, message *client.Message) bool {
	key := fmt.Sprintf("%s:%d", forwardKey, message.MediaAlbumId)
	item, ok := mediaAlbums[key]
	if !ok {
		item = MediaAlbum{}
	}
	item.messages = append(item.messages, message)
	item.lastReceived = time.Now()
	mediaAlbums[key] = item
	return !ok
}

func getMediaAlbumLastReceivedDiff(key string) time.Duration {
	mediaAlbumsMu.Lock()
	defer mediaAlbumsMu.Unlock()
	return time.Since(mediaAlbums[key].lastReceived)
}

func getMediaAlbumMessages(key string) []*client.Message {
	mediaAlbumsMu.Lock()
	defer mediaAlbumsMu.Unlock()
	messages := mediaAlbums[key].messages
	delete(mediaAlbums, key)
	return messages
}

const waitForMediaAlbum = 3 * time.Second

func handleMediaAlbum(forwardKey string, id client.JsonInt64, cb func(messages []*client.Message)) {
	key := fmt.Sprintf("%s:%d", forwardKey, id)
	diff := getMediaAlbumLastReceivedDiff(key)
	if diff < waitForMediaAlbum {
		time.Sleep(waitForMediaAlbum - diff)
		handleMediaAlbum(forwardKey, id, cb)
		return
	}
	messages := getMediaAlbumMessages(key)
	cb(messages)
}

func doUpdateNewMessage(messages []*client.Message, forwardKey string, forward config.Forward, forwardedTo map[int64]bool, checkFns map[int64]func(), otherFns map[int64]func()) {
	src := messages[0]
	formattedText, contentMode := getFormattedText(src.Content)
	log.Printf("doUpdateNewMessage > do > ChatId: %d Id: %d hasText: %t MediaAlbumId: %d", src.ChatId, src.Id, formattedText.Text != "", src.MediaAlbumId)
	// for log
	var (
		isFilters = false
		isOther   = false
		result    []int64
	)
	defer func() {
		log.Printf("doUpdateNewMessage > ok > ChatId: %d Id: %d isFilters: %t isOther: %t result: %v", src.ChatId, src.Id, isFilters, isOther, result)
	}()
	if contentMode == "" {
		log.Print("contentMode == \"\"")
		return
	}
	switch checkFilters(formattedText, forward) {
	case FiltersOK:
		isFilters = true
		// checkFns[forward.Check] = nil // !! не надо сбрасывать - хочу проверить сообщение, даже если где-то прошли фильтры
		otherFns[forward.Other] = nil
		for _, dstChatId := range forward.To {
			if isNotForwardedTo(forwardedTo, dstChatId) {
				forwardNewMessages(tdlibClient, messages, src.ChatId, dstChatId, forward.SendCopy, forwardKey)
				result = append(result, dstChatId)
			}
		}
	case FiltersCheck:
		if forward.Check != 0 {
			_, ok := checkFns[forward.Check]
			if !ok {
				checkFns[forward.Check] = func() {
					const isSendCopy = false // обязательно надо форвардить, иначе невидно текущего сообщения
					forwardNewMessages(tdlibClient, messages, src.ChatId, forward.Check, isSendCopy, forwardKey)
				}
			}
		}
	case FiltersOther:
		if forward.Other != 0 {
			_, ok := otherFns[forward.Other]
			if !ok {
				otherFns[forward.Other] = func() {
					const isSendCopy = true // обязательно надо копировать, иначе невидно редактирование исходного сообщения
					forwardNewMessages(tdlibClient, messages, src.ChatId, forward.Other, isSendCopy, forwardKey)
				}
			}
		}
	}
}

// func getConfig() *config.Config {
// 	configMu.Lock()
// 	defer configMu.Unlock()
// 	result := configData // ???
// 	return result
// }

func handlePanic() {
	if err := recover(); err != nil {
		log.Printf("Panic...\n%s\n\n%s", err, debug.Stack())
		os.Exit(1)
	}
}

const viewedMessagesPrefix = "viewedMsgs"

func incrementViewedMessages(toChatId int64) {
	date := time.Now().UTC().Format("2006-01-02")
	key := []byte(fmt.Sprintf("%s:%d:%s", viewedMessagesPrefix, toChatId, date))
	val := incrementByDB(key)
	log.Printf("incrementViewedMessages > key: %s val: %d", key, int64(bytesToUint64(val)))
}

const forwardedMessagesPrefix = "forwardedMsgs"

func incrementForwardedMessages(toChatId int64) {
	date := time.Now().UTC().Format("2006-01-02")
	key := []byte(fmt.Sprintf("%s:%d:%s", forwardedMessagesPrefix, toChatId, date))
	val := incrementByDB(key)
	log.Printf("incrementForwardedMessages > key: %s val: %d", key, int64(bytesToUint64(val)))
}

var forwardedToMu sync.Mutex

func isNotForwardedTo(forwardedTo map[int64]bool, dstChatId int64) bool {
	forwardedToMu.Lock()
	defer forwardedToMu.Unlock()
	if !forwardedTo[dstChatId] {
		forwardedTo[dstChatId] = true
		return true
	}
	return false
}

// **** db routines

func uint64ToBytes(i uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], i)
	return buf[:]
}

func bytesToUint64(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}

func incrementByDB(key []byte) []byte {
	// Merge function to add two uint64 numbers
	add := func(existing, new []byte) []byte {
		return uint64ToBytes(bytesToUint64(existing) + bytesToUint64(new))
	}
	m := badgerDB.GetMergeOperator(key, add, 200*time.Millisecond)
	defer m.Stop()
	m.Add(uint64ToBytes(1))
	result, _ := m.Get()
	return result
}

func getForDB(key []byte) []byte {
	var (
		err error
		val []byte
	)
	err = badgerDB.View(func(txn *badger.Txn) error {
		var item *badger.Item
		item, err = txn.Get(key)
		if err != nil {
			return err
		}
		val, err = item.ValueCopy(nil)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		log.Printf("getForDB > key: %s %s", key, err)
	} else {
		log.Printf("getForDB > key: %s, val: %s", key, string(val))
	}
	return val
}

func setForDB(key []byte, val []byte) {
	err := badgerDB.Update(func(txn *badger.Txn) error {
		err := txn.Set(key, val)
		return err
	})
	if err != nil {
		log.Printf("setForDB > key: %s err: %s", string(key), err)
	} else {
		log.Printf("setForDB > key: %s val: %s", string(key), string(val))
	}
}

func deleteForDB(key []byte) {
	err := badgerDB.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
	if err != nil {
		log.Printf("deleteForDB > key: %s err: %s", string(key), err)
	}
}

// TODO: потокобезопасное взаимодействие с queue?

var queue = list.New()

func runQueue() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for t := range ticker.C {
		_ = t
		// log.Print(t.UTC().Second())
		front := queue.Front()
		if front != nil {
			fn := front.Value.(func())
			fn()
			// This will remove the allocated memory and avoid memory leaks
			queue.Remove(front)
		}
	}
}

func distinct(a []string) []string {
	set := make(map[string]struct{})
	for _, val := range a {
		set[val] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	return result
}

const waitForForward = 3 * time.Second // чтобы бот успел отреагировать на сообщение

func getInputThumbnail(thumbnail *client.Thumbnail) *client.InputThumbnail {
	if thumbnail == nil || thumbnail.File == nil && thumbnail.File.Remote == nil {
		return nil
	}
	return &client.InputThumbnail{
		Thumbnail: &client.InputFileRemote{
			Id: thumbnail.File.Remote.Id,
		},
		Width:  thumbnail.Width,
		Height: thumbnail.Height,
	}
}

const answerMessageIdPrefix = "answerMsgId"

func setAnswerMessageId(dstChatId, tmpMessageId int64, fromChatMessageId string) {
	key := []byte(fmt.Sprintf("%s:%d:%d", answerMessageIdPrefix, dstChatId, tmpMessageId))
	val := []byte(fromChatMessageId)
	setForDB(key, val)
}

func getAnswerMessageId(dstChatId, tmpMessageId int64) string {
	key := []byte(fmt.Sprintf("%s:%d:%d", answerMessageIdPrefix, dstChatId, tmpMessageId))
	val := getForDB(key)
	return string(val)
}

func deleteAnswerMessageId(dstChatId, tmpMessageId int64) {
	key := []byte(fmt.Sprintf("%s:%d:%d", answerMessageIdPrefix, dstChatId, tmpMessageId))
	deleteForDB(key)
}

func addAutoAnswer(formattedText *client.FormattedText, src *client.Message) {
	if configAnswer, ok := configData.Answers[src.ChatId]; ok && configAnswer.Auto {
		if data, ok := getReplyMarkupData(src); ok {
			if answer, err := tdlibClient.GetCallbackQueryAnswer(
				&client.GetCallbackQueryAnswerRequest{
					ChatId:    src.ChatId,
					MessageId: src.Id,
					Payload:   &client.CallbackQueryPayloadData{Data: data},
				},
			); err != nil {
				log.Print(err)
			} else {
				sourceAnswer, err := tdlibClient.ParseTextEntities(&client.ParseTextEntitiesRequest{
					Text: escapeAll(answer.Text),
					ParseMode: &client.TextParseModeMarkdown{
						Version: 2,
					},
				})
				if err != nil {
					log.Print("ParseTextEntities > ", err)
				} else {
					offset := int32(utils.StrLen(formattedText.Text))
					if offset > 0 {
						formattedText.Text += "\n\n"
						offset = offset + 2
					}
					for _, entity := range sourceAnswer.Entities {
						entity.Offset += offset
					}
					formattedText.Text += sourceAnswer.Text
					formattedText.Entities = append(formattedText.Entities, sourceAnswer.Entities...)
				}
				log.Printf("addAutoAnswer > %#v", formattedText)
			}
		}
	}
}

func addSourceSign(formattedText *client.FormattedText, title string) {
	sourceSign, err := tdlibClient.ParseTextEntities(&client.ParseTextEntitiesRequest{
		Text: title,
		ParseMode: &client.TextParseModeMarkdown{
			Version: 2,
		},
	})
	if err != nil {
		log.Print("ParseTextEntities > ", err)
	} else {
		offset := int32(utils.StrLen(formattedText.Text))
		if offset > 0 {
			formattedText.Text += "\n\n"
			offset = offset + 2
		}
		for _, entity := range sourceSign.Entities {
			entity.Offset += offset
		}
		formattedText.Text += sourceSign.Text
		formattedText.Entities = append(formattedText.Entities, sourceSign.Entities...)
	}
	log.Printf("addSourceSign > %#v", formattedText)
}

func addSourceLink(message *client.Message, formattedText *client.FormattedText, title string) {
	messageLink, err := tdlibClient.GetMessageLink(&client.GetMessageLinkRequest{
		ChatId:     message.ChatId,
		MessageId:  message.Id,
		ForAlbum:   message.MediaAlbumId != 0,
		ForComment: false,
	})
	if err != nil {
		log.Printf("GetMessageLink > ChatId: %d MessageId: %d %s", message.ChatId, message.Id, err)
	} else {
		sourceLink, err := tdlibClient.ParseTextEntities(&client.ParseTextEntitiesRequest{
			Text: fmt.Sprintf("[%s%s](%s)", "\U0001f517", title, messageLink.Link),
			ParseMode: &client.TextParseModeMarkdown{
				Version: 2,
			},
		})
		if err != nil {
			log.Print("ParseTextEntities > ", err)
		} else {
			// TODO: тут упало на опросе https://t.me/Full_Time_Trading/40922
			offset := int32(utils.StrLen(formattedText.Text))
			if offset > 0 {
				formattedText.Text += "\n\n"
				offset = offset + 2
			}
			for _, entity := range sourceLink.Entities {
				entity.Offset += offset
			}
			formattedText.Text += sourceLink.Text
			formattedText.Entities = append(formattedText.Entities, sourceLink.Entities...)
		}
	}
	log.Printf("addSourceLink > %#v", formattedText)
}

func getInputMessageContent(messageContent client.MessageContent, formattedText *client.FormattedText, contentMode ContentMode) client.InputMessageContent {
	switch contentMode {
	case ContentModeText:
		messageText := messageContent.(*client.MessageText)
		return &client.InputMessageText{
			Text:                  formattedText,
			DisableWebPagePreview: messageText.WebPage == nil || messageText.WebPage.Url == "",
			ClearDraft:            true,
		}
	case ContentModeAnimation:
		messageAnimation := messageContent.(*client.MessageAnimation)
		return &client.InputMessageAnimation{
			Animation: &client.InputFileRemote{
				Id: messageAnimation.Animation.Animation.Remote.Id,
			},
			// TODO: AddedStickerFileIds , // if applicable?
			Duration: messageAnimation.Animation.Duration,
			Width:    messageAnimation.Animation.Width,
			Height:   messageAnimation.Animation.Height,
			Caption:  formattedText,
		}
	case ContentModeAudio:
		messageAudio := messageContent.(*client.MessageAudio)
		return &client.InputMessageAudio{
			Audio: &client.InputFileRemote{
				Id: messageAudio.Audio.Audio.Remote.Id,
			},
			AlbumCoverThumbnail: getInputThumbnail(messageAudio.Audio.AlbumCoverThumbnail),
			Title:               messageAudio.Audio.Title,
			Duration:            messageAudio.Audio.Duration,
			Performer:           messageAudio.Audio.Performer,
			Caption:             formattedText,
		}
	case ContentModeDocument:
		messageDocument := messageContent.(*client.MessageDocument)
		return &client.InputMessageDocument{
			Document: &client.InputFileRemote{
				Id: messageDocument.Document.Document.Remote.Id,
			},
			Thumbnail: getInputThumbnail(messageDocument.Document.Thumbnail),
			Caption:   formattedText,
		}
	case ContentModePhoto:
		messagePhoto := messageContent.(*client.MessagePhoto)
		return &client.InputMessagePhoto{
			Photo: &client.InputFileRemote{
				Id: messagePhoto.Photo.Sizes[0].Photo.Remote.Id,
			},
			// Thumbnail: , // https://github.com/tdlib/td/issues/1505
			// A: if you use InputFileRemote, then there is no way to change the thumbnail, so there are no reasons to specify it.
			// TODO: AddedStickerFileIds: ,
			Width:   messagePhoto.Photo.Sizes[0].Width,
			Height:  messagePhoto.Photo.Sizes[0].Height,
			Caption: formattedText,
			// Ttl: ,
		}
	case ContentModeVideo:
		messageVideo := messageContent.(*client.MessageVideo)
		// TODO: https://github.com/tdlib/td/issues/1504
		// var stickerSets *client.StickerSets
		// var AddedStickerFileIds []int32 // ????
		// if messageVideo.Video.HasStickers {
		// 	var err error
		// 	stickerSets, err = tdlibClient.GetAttachedStickerSets(&client.GetAttachedStickerSetsRequest{
		// 		FileId: messageVideo.Video.Video.Id,
		// 	})
		// 	if err != nil {
		// 		log.Print("GetAttachedStickerSets > ", err)
		// 	}
		// }
		return &client.InputMessageVideo{
			Video: &client.InputFileRemote{
				Id: messageVideo.Video.Video.Remote.Id,
			},
			Thumbnail: getInputThumbnail(messageVideo.Video.Thumbnail),
			// TODO: AddedStickerFileIds: ,
			Duration:          messageVideo.Video.Duration,
			Width:             messageVideo.Video.Width,
			Height:            messageVideo.Video.Height,
			SupportsStreaming: messageVideo.Video.SupportsStreaming,
			Caption:           formattedText,
			// Ttl: ,
		}
	case ContentModeVoiceNote:
		return &client.InputMessageVoiceNote{
			// TODO: support ContentModeVoiceNote
			// VoiceNote: ,
			// Duration: ,
			// Waveform: ,
			Caption: formattedText,
		}
	}
	return nil
}

func replaceMyselfLinks(formattedText *client.FormattedText, srcChatId, dstChatId int64) {
	if data, ok := configData.ReplaceMyselfLinks[dstChatId]; ok {
		log.Printf("replaceMyselfLinks > srcChatId: %d dstChatId: %d", srcChatId, dstChatId)
		for _, entity := range formattedText.Entities {
			if textUrl, ok := entity.Type.(*client.TextEntityTypeTextUrl); ok {
				if messageLinkInfo, err := tdlibClient.GetMessageLinkInfo(&client.GetMessageLinkInfoRequest{
					Url: textUrl.Url,
				}); err != nil {
					log.Print("GetMessageLinkInfo > ", err)
				} else {
					src := messageLinkInfo.Message
					if src != nil && srcChatId == src.ChatId {
						isReplaced := false
						fromChatMessageId := fmt.Sprintf("%d:%d", src.ChatId, src.Id)
						toChatMessageIds := getCopiedMessageIds(fromChatMessageId)
						log.Printf("fromChatMessageId: %s toChatMessageIds: %v", fromChatMessageId, toChatMessageIds)
						var tmpMessageId int64 = 0
						for _, toChatMessageId := range toChatMessageIds {
							a := strings.Split(toChatMessageId, ":")
							if int64(convertToInt(a[1])) == dstChatId {
								tmpMessageId = int64(convertToInt(a[2]))
								break
							}
						}
						if tmpMessageId != 0 {
							if messageLink, err := tdlibClient.GetMessageLink(&client.GetMessageLinkRequest{
								ChatId:    dstChatId,
								MessageId: getNewMessageId(dstChatId, tmpMessageId),
							}); err != nil {
								log.Print("GetMessageLink > ", err)
							} else {
								entity.Type = &client.TextEntityTypeTextUrl{
									Url: messageLink.Link,
								}
								isReplaced = true
							}
						}
						if !isReplaced && data.DeleteExternal {
							entity.Type = &client.TextEntityTypeStrikethrough{}
						}
					}
				}
			}
		}
	}
}

func copyFormattedText(formattedText *client.FormattedText) *client.FormattedText {
	result := *formattedText
	return &result
}

func escapeAll(s string) string {
	// эскейпит все символы: которые нужны для markdown-разметки
	a := []string{
		"_",
		"*",
		`\[`,
		`\]`,
		"(",
		")",
		"~",
		"`",
		">",
		"#",
		"+",
		`\-`,
		"=",
		"|",
		"{",
		"}",
		".",
		"!",
	}
	re := regexp.MustCompile("[" + strings.Join(a, "|") + "]")
	return re.ReplaceAllString(s, `\$0`)
}

func addSources(formattedText *client.FormattedText, src *client.Message, dstChatId int64) {
	if source, ok := configData.Sources[src.ChatId]; ok {
		if containsInt64(source.Sign.For, dstChatId) {
			addSourceSign(formattedText, source.Sign.Title)
		} else if containsInt64(source.Link.For, dstChatId) {
			addSourceLink(src, formattedText, source.Link.Title)
		}
	}
}

// func resetEntities(formattedText *client.FormattedText, dstChatId int64) {
//	// withResetEntities := containsInt64(configData.ResetEntities, dstChatId)
// 	if result, err := tdlibClient.ParseTextEntities(&client.ParseTextEntitiesRequest{
// 		Text: escapeAll(formattedText.Text),
// 		ParseMode: &client.TextParseModeMarkdown{
// 			Version: 2,
// 		},
// 	}); err != nil {
// 		log.Print(err)
// 	} else {
// 		*formattedText = *result
// 	}
// }

func replaceFragments(formattedText *client.FormattedText, dstChatId int64) {
	if data, ok := configData.ReplaceFragments[dstChatId]; ok {
		isReplaced := false
		for from, to := range data {
			re := regexp.MustCompile("(?i)" + from)
			if re.FindString(formattedText.Text) != "" {
				isReplaced = true
				// вынес в конфиг
				// if utils.StrLen(from) != utils.StrLen(to) {
				// 	log.Print("error: utils.StrLen(from) != utils.StrLen(to)")
				// 	to = strings.Repeat(".", utils.StrLen(from))
				// }
				formattedText.Text = re.ReplaceAllString(formattedText.Text, to)
			}
		}
		if isReplaced {
			log.Print("isReplaced")
		}
	}
}

// func replaceFragments2(formattedText *client.FormattedText, dstChatId int64) {
// 	if replaceFragments, ok := configData.ReplaceFragments[dstChatId]; ok {
// 		// TODO: нужно реализовать свою версию GetMarkdownText,
// 		// которая будет обрабатывать вложенные markdown-entities и экранировать markdown-элементы
// 		// https://github.com/tdlib/td/issues/1564
// 		log.Print(formattedText.Text)
// 		if markdownText, err := tdlibClient.GetMarkdownText(&client.GetMarkdownTextRequest{Text: formattedText}); err != nil {
// 			log.Print(err)
// 		} else {
// 			log.Print(markdownText.Text)
// 			isReplaced := false
// 			for from, to := range replaceFragments {
// 				re := regexp.MustCompile("(?i)" + from)
// 				if re.FindString(markdownText.Text) != "" {
// 					isReplaced = true
// 					markdownText.Text = re.ReplaceAllString(markdownText.Text, to)
// 				}
// 			}
// 			if isReplaced {
// 				var err error
// 				result, err := tdlibClient.ParseMarkdown(
// 					&client.ParseMarkdownRequest{
// 						Text: markdownText,
// 					},
// 				)
// 				if err != nil {
// 					log.Print(err)
// 				}
// 				*formattedText = *result
// 			}
// 		}
// 	}
// }
