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
	utf16 "unicode/utf16"

	"github.com/comerc/budva32/config"
	"github.com/dgraph-io/badger"
	"github.com/joho/godotenv"
	"github.com/zelenin/go-tdlib/client"
)

// TODO: при копировании теряется картинка (заменяется на предпросмотр ссылки) https://t.me/Full_Time_Trading/46292
// TODO: https://telegram.org/blog/payments-2-0-scheduled-voice-chats/ru
// TODO: ОГРОМНОЕ ТОРНАДО ПРОШЛО В ВЕРНОНЕ - похерился американский флаг при копировании на мобильной версии
// TODO: как бороться с зацикливанием пересылки
// TODO: падает при удалении целевого чата?
// TODO: edit & delete требуют ожидания waitForForward и накапливаемого waitForMediaAlbum (или забить?)
// TODO: вынести waitForForward в конфиг (не для всех каналов требуется ожидание реакции бота)
// TODO: фильтры, как исполняемые скрипты на node.js
// TODO: ротация лога
// TODO: синхронизировать закреп сообщений
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
		tmp, err := config.Load()
		if err != nil {
			log.Printf("Can't initialise config: %s", err)
			return
		}
		// configMu.Lock()
		// defer configMu.Unlock()
		configData = tmp
	})

	go func() {
		http.HandleFunc("/favicon.ico", getFaviconHandler)
		http.HandleFunc("/", withBasicAuth(withAuthentiation(getChatsHandler)))
		http.HandleFunc("/ping", getPingHandler)
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
			if updateNewMessage, ok := update.(*client.UpdateNewMessage); ok {
				src := updateNewMessage.Message
				isExist := false
				otherFns := make(map[int64]func())
				forwardedTo := make(map[int64]bool)
				var wg sync.WaitGroup
				// configData := getConfig()
				for i, forward := range configData.Forwards {
					if src.ChatId == forward.From && src.CanBeForwarded {
						isExist = true
						for _, dscChatId := range forward.To {
							_, isPresent := forwardedTo[dscChatId]
							if !isPresent {
								forwardedTo[dscChatId] = false
							}
						}
						if src.MediaAlbumId == 0 {
							wg.Add(1)
							log.Print("wg.Add(1) for src.Id: ", src.Id)
							forward := forward // !!!! copy for go routine
							fn := func() {
								defer func() {
									wg.Done()
									log.Print("wg.Done() for src.Id: ", src.Id)
								}()
								doUpdateNewMessage([]*client.Message{src}, forward, forwardedTo, otherFns)
							}
							queue.PushBack(fn)
						} else {
							isFirstMessage := addMessageToMediaAlbum(i, src)
							if isFirstMessage {
								wg.Add(1)
								log.Print("wg.Add(1) for src.Id: ", src.Id)
								forward := forward // !!!! copy for go routine
								go handleMediaAlbum(i, src.MediaAlbumId,
									func(messages []*client.Message) {
										fn := func() {
											defer func() {
												wg.Done()
												log.Print("wg.Done() for src.Id: ", src.Id)
											}()
											doUpdateNewMessage(messages, forward, forwardedTo, otherFns)
										}
										queue.PushBack(fn)
									})
							}
						}
					}
				}
				if isExist {
					go func() {
						wg.Wait()
						log.Print("wg.Wait() for src.Id: ", src.Id)
						for dscChatId, isForwarded := range forwardedTo {
							if isForwarded {
								incrementForwardedMessages(dscChatId)
							}
							incrementViewedMessages(dscChatId)
						}
						for other, fn := range otherFns {
							if fn == nil {
								log.Printf("other: %d nil", other)
								continue
							}
							log.Printf("other: %d fn()", other)
							fn()
						}
					}()
				}
			} else if updateMessageEdited, ok := update.(*client.UpdateMessageEdited); ok {
				chatId := updateMessageEdited.ChatId
				messageId := updateMessageEdited.MessageId
				fn := func() {
					var result []string
					fromChatMessageId := fmt.Sprintf("%d:%d", chatId, messageId)
					toChatMessageIds := getCopiedMessageIds(fromChatMessageId)
					log.Printf("updateMessageEdited go fromChatMessageId: %s toChatMessageIds: %v", fromChatMessageId, toChatMessageIds)
					defer func() {
						log.Printf("updateMessageEdited ok result: %v", result)
					}()
					if len(toChatMessageIds) == 0 {
						return
					}
					src, err := tdlibClient.GetMessage(&client.GetMessageRequest{
						ChatId:    chatId,
						MessageId: messageId,
					})
					if err != nil {
						log.Print("GetMessage() src ", err)
						return
					}
					srcFormattedText, contentMode := getFormattedText(src.Content)
					log.Printf("srcChatId: %d srcId: %d hasText: %t MediaAlbumId: %d", src.ChatId, src.Id, srcFormattedText != nil && srcFormattedText.Text != "", src.MediaAlbumId)
					for _, toChatMessageId := range toChatMessageIds {
						a := strings.Split(toChatMessageId, ":")
						dscChatId := int64(convertToInt(a[0]))
						dscId := int64(convertToInt(a[1]))
						formattedText := copyFormattedText(srcFormattedText)
						if replaceMyselfLink, ok := configData.ReplaceMyselfLinks[dscChatId]; ok {
							replaceMyselfLinks(formattedText, src.ChatId, dscChatId, replaceMyselfLink.DeleteExternal)
						}
						if source, ok := configData.Sources[src.ChatId]; ok {
							if containsInt64(source.Sign.For, dscChatId) {
								addSourceSign(formattedText, source.Sign.Title)
							}
							if containsInt64(source.Link.For, dscChatId) {
								addSourceLink(src, formattedText, source.Link.Title)
							}
						}
						newMessageId := getNewMessageId(dscChatId, dscId)
						result = append(result, fmt.Sprintf("toChatMessageId: %s, newMessageId: %d", toChatMessageId, newMessageId))
						log.Print("contentMode: ", contentMode)
						switch contentMode {
						case ContentModeText:
							content := getInputMessageContent(src.Content, formattedText, contentMode)
							dsc, err := tdlibClient.EditMessageText(&client.EditMessageTextRequest{
								ChatId:              dscChatId,
								MessageId:           newMessageId,
								InputMessageContent: content,
							})
							if err != nil {
								log.Print("EditMessageText() ", err)
							}
							log.Printf("EditMessageText() dsc: %#v", dsc)
						case ContentModeAnimation:
						case ContentModeDocument:
						case ContentModeAudio:
						case ContentModeVideo:
						case ContentModePhoto:
							content := getInputMessageContent(src.Content, formattedText, contentMode)
							dsc, err := tdlibClient.EditMessageMedia(&client.EditMessageMediaRequest{
								ChatId:              dscChatId,
								MessageId:           newMessageId,
								InputMessageContent: content,
							})
							if err != nil {
								log.Print("EditMessageMedia() ", err)
							}
							log.Printf("EditMessageMedia() dsc: %#v", dsc)
						case ContentModeVoiceNote:
							dsc, err := tdlibClient.EditMessageCaption(&client.EditMessageCaptionRequest{
								ChatId:    dscChatId,
								MessageId: newMessageId,
								Caption:   formattedText,
							})
							if err != nil {
								log.Print("EditMessageCaption() ", err)
							}
							log.Printf("EditMessageCaption() dsc: %#v", dsc)
						}
					}
				}
				queue.PushBack(fn)
			} else if updateMessageSendSucceeded, ok := update.(*client.UpdateMessageSendSucceeded); ok {
				log.Print("updateMessageSendSucceeded go")
				message := updateMessageSendSucceeded.Message
				setNewMessageId(message.ChatId, updateMessageSendSucceeded.OldMessageId, message.Id)
				log.Print("updateMessageSendSucceeded ok")
			} else if updateDeleteMessages, ok := update.(*client.UpdateDeleteMessages); ok && updateDeleteMessages.IsPermanent {
				chatId := updateDeleteMessages.ChatId
				messageIds := updateDeleteMessages.MessageIds
				fn := func() {
					var result []string
					log.Printf("updateDeleteMessages go chatId: %d messageIds: %v", chatId, messageIds)
					defer func() {
						log.Printf("updateDeleteMessages ok result: %v", result)
					}()
					for _, messageId := range messageIds {
						fromChatMessageId := fmt.Sprintf("%d:%d", chatId, messageId)
						toChatMessageIds := getCopiedMessageIds(fromChatMessageId)
						deleteCopiedMessageIds(fromChatMessageId)
						for _, toChatMessageId := range toChatMessageIds {
							a := strings.Split(toChatMessageId, ":")
							dscChatId := int64(convertToInt(a[0]))
							dscId := int64(convertToInt(a[1]))
							newMessageId := getNewMessageId(dscChatId, dscId)
							_, err := tdlibClient.DeleteMessages(&client.DeleteMessagesRequest{
								ChatId:     dscChatId,
								MessageIds: []int64{newMessageId},
								Revoke:     true,
							})
							if err != nil {
								log.Print("DeleteMessages() ", err)
								continue
							}
							result = append(result, fmt.Sprintf("%d:%d", dscChatId, newMessageId))
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
					val := getByDB(key)
					if len(val) == 0 {
						viewed = 0
					} else {
						viewed = int64(bytesToUint64(val))
					}
				}
				{
					key := []byte(fmt.Sprintf("%s:%d:%s", forwardedMessagesPrefix, toChatId, date))
					val := getByDB(key)
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
					log.Print("ParseTextEntities() ", err)
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
						log.Print("SendMessage() ", err)
					}
				}
			}
		}
	}
}

func convertToInt(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		log.Print("convertToInt() ", err)
		return 0
	}
	return int(i)
}

// ****

// type ChatMessageId string // ChatId:MessageId

// var copiedMessageIds = make(map[ChatMessageId][]ChatMessageId) // [From][]To

const copiedMessageIdsPrefix = "copiedMsgIds"

func deleteCopiedMessageIds(fromChatMessageId string) {
	key := []byte(fmt.Sprintf("%s:%s", copiedMessageIdsPrefix, fromChatMessageId))
	err := badgerDB.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
	if err != nil {
		log.Print(err)
	}
	log.Printf("deleteCopiedMessageIds() fromChatMessageId: %s", fromChatMessageId)
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
		log.Print("setCopiedMessageId() ", err)
	}
	log.Printf("setCopiedMessageId() fromChatMessageId: %s toChatMessageId: %s val: %s", fromChatMessageId, toChatMessageId, val)
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
		log.Print("getCopiedMessageIds() ", err)
	}
	toChatMessageIds := []string{}
	s := fmt.Sprintf("%s", val)
	if s != "" {
		// workaround https://stackoverflow.com/questions/28330908/how-to-string-split-an-empty-string-in-go
		toChatMessageIds = strings.Split(s, ",")
	}
	log.Printf("getCopiedMessageIds() fromChatMessageId: %s toChatMessageIds: %v", fromChatMessageId, toChatMessageIds)
	return toChatMessageIds
}

// var newMessageIds = make(map[ChatMessageId]int64)

const newMessageIdPrefix = "newMsgId"

func setNewMessageId(chatId, tmpMessageId, newMessageId int64) {
	key := []byte(fmt.Sprintf("%s:%d:%d", newMessageIdPrefix, chatId, tmpMessageId))
	val := []byte(fmt.Sprintf("%d", newMessageId))
	err := badgerDB.Update(func(txn *badger.Txn) error {
		err := txn.Set(key, val)
		return err
	})
	if err != nil {
		log.Print("setNewMessageId() ", err)
	}
	log.Printf("setNewMessageId() key: %d:%d val: %d", chatId, tmpMessageId, newMessageId)
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
		log.Print("getNewMessageId() ", err)
		return 0
	}
	newMessageId := int64(convertToInt(fmt.Sprintf("%s", val)))
	log.Printf("getNewMessageId() key: %d:%d val: %d", chatId, tmpMessageId, newMessageId)
	return newMessageId
	// return newMessageIds[ChatMessageId(fmt.Sprintf("%d:%d", chatId, tmpMessageId))]
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

func forwardNewMessages(tdlibClient *client.Client, messages []*client.Message, srcChatId, dscChatId int64, forward config.Forward) {
	log.Printf("forwardNewMessages() srcChatId: %d dscChatId: %d", srcChatId, dscChatId)
	diff := getLastForwardedDiff(dscChatId)
	if diff < waitForForward {
		time.Sleep(waitForForward - diff)
	}
	setLastForwarded(dscChatId)
	var (
		result *client.Messages
		err    error
	)
	if forward.SendCopy {
		result, err = sendCopyNewMessages(tdlibClient, messages, srcChatId, dscChatId, forward)
	} else {
		result, err = tdlibClient.ForwardMessages(&client.ForwardMessagesRequest{
			ChatId:     dscChatId,
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
		log.Print("forwardNewMessages() ", err)
	} else if len(result.Messages) != int(result.TotalCount) || result.TotalCount == 0 {
		log.Print("forwardNewMessages(): invalid TotalCount")
	} else if len(result.Messages) != len(messages) {
		log.Print("forwardNewMessages(): invalid len(messages)")
	} else if forward.SendCopy {
		for i, dsc := range result.Messages {
			if dsc == nil {
				log.Printf("!!!! dsc == nil !!!! result: %#v messages: %#v", result, messages)
				continue
			}
			dscId := dsc.Id
			src := messages[i] // !!!! for origin message
			toChatMessageId := fmt.Sprintf("%d:%d", dscChatId, dscId)
			fromChatMessageId := fmt.Sprintf("%d:%d", src.ChatId, src.Id)
			setCopiedMessageId(fromChatMessageId, toChatMessageId)
		}
	}
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
		formattedText = &client.FormattedText{}
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

func checkFilters(formattedText *client.FormattedText, forward config.Forward, isOther *bool) bool {
	*isOther = false
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
			*isOther = true
			return false
		}
	} else {
		if forward.Exclude != "" {
			re := regexp.MustCompile("(?i)" + forward.Exclude)
			if re.FindString(formattedText.Text) != "" {
				return false
			}
		}
		hasInclude := false
		if forward.Include != "" {
			hasInclude = true
			re := regexp.MustCompile("(?i)" + forward.Include)
			if re.FindString(formattedText.Text) != "" {
				return true
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
						return true
					}
				}
			}
		}
		if hasInclude {
			*isOther = true
			return false
		}
	}
	return true
}

func withBasicAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(user), []byte(os.Getenv("BUDVA32_USER"))) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(os.Getenv("BUDVA32_PASS"))) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Please enter your username and password"`)
			w.WriteHeader(401)
			w.Write([]byte("You are unauthorized to access the application.\n"))
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

func getPingHandler(w http.ResponseWriter, r *http.Request) {
	ret, err := time.Now().UTC().MarshalJSON()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
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
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
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
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
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

var mediaAlbums = make(map[string]MediaAlbum) // int : client.JsonInt64

// https://github.com/tdlib/td/issues/1482
func addMessageToMediaAlbum(i int, message *client.Message) bool {
	key := fmt.Sprintf("%d:%d", i, message.MediaAlbumId)
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

func handleMediaAlbum(i int, id client.JsonInt64, cb func(messages []*client.Message)) {
	key := fmt.Sprintf("%d:%d", i, id)
	diff := getMediaAlbumLastReceivedDiff(key)
	if diff < waitForMediaAlbum {
		time.Sleep(waitForMediaAlbum - diff)
		handleMediaAlbum(i, id, cb)
		return
	}
	messages := getMediaAlbumMessages(key)
	cb(messages)
}

func doUpdateNewMessage(messages []*client.Message, forward config.Forward, forwardedTo map[int64]bool, otherFns map[int64]func()) {
	src := messages[0]
	formattedText, contentMode := getFormattedText(src.Content)
	log.Printf("updateNewMessage go ChatId: %d Id: %d hasText: %t MediaAlbumId: %d", src.ChatId, src.Id, formattedText.Text != "", src.MediaAlbumId)
	// for log
	var (
		isFilters = false
		isOther   = false
		result    []int64
	)
	defer func() {
		log.Printf("updateNewMessage ok ChatId: %d Id: %d isFilters: %t isOther: %t result: %v", src.ChatId, src.Id, isFilters, isOther, result)
	}()
	if contentMode == "" {
		log.Print("contentMode == \"\"")
		return
	}
	if checkFilters(formattedText, forward, &isOther) {
		isFilters = true
		otherFns[forward.Other] = nil
		for _, dscChatId := range forward.To {
			if isNotForwardedTo(forwardedTo, dscChatId) {
				forwardNewMessages(tdlibClient, messages, src.ChatId, dscChatId, forward)
				result = append(result, dscChatId)
			}
		}
	} else if isOther && forward.Other != 0 {
		_, ok := otherFns[forward.Other]
		if !ok {
			otherFns[forward.Other] = func() {
				dscChatId := forward.Other
				forwardNewMessages(tdlibClient, messages, src.ChatId, dscChatId, forward)
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
	log.Printf("incrementViewedMessages() key: %s val: %d", key, int64(bytesToUint64(val)))
}

const forwardedMessagesPrefix = "forwardedMsgs"

func incrementForwardedMessages(toChatId int64) {
	date := time.Now().UTC().Format("2006-01-02")
	key := []byte(fmt.Sprintf("%s:%d:%s", forwardedMessagesPrefix, toChatId, date))
	val := incrementByDB(key)
	log.Printf("incrementForwardedMessages() key: %s val: %d", key, int64(bytesToUint64(val)))
}

var forwardedToMu sync.Mutex

func isNotForwardedTo(forwardedTo map[int64]bool, dscChatId int64) bool {
	forwardedToMu.Lock()
	defer forwardedToMu.Unlock()
	if !forwardedTo[dscChatId] {
		forwardedTo[dscChatId] = true
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

func getByDB(key []byte) []byte {
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
		log.Printf("getByDB() key: %s %s", key, err)
	} else {
		log.Printf("getByDB() key: %s, val: %#v", key, val)
	}
	return val
}

// func deleteByDB(key []byte) {
// 	err := badgerDB.Update(func(txn *badger.Txn) error {
// 		return txn.Delete(key)
// 	})
// 	if err != nil {
// 		log.Print(err)
// 	}
// }

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

func addSourceSign(formattedText *client.FormattedText, title string) {
	sourceSign, err := tdlibClient.ParseTextEntities(&client.ParseTextEntitiesRequest{
		Text: title,
		ParseMode: &client.TextParseModeMarkdown{
			Version: 2,
		},
	})
	if err != nil {
		log.Print("ParseTextEntities() ", err)
	} else {
		offset := int32(len(utf16.Encode([]rune(formattedText.Text))))
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
	log.Printf("addSourceSign() %#v", formattedText)
}

func addSourceLink(message *client.Message, formattedText *client.FormattedText, title string) {
	messageLink, err := tdlibClient.GetMessageLink(&client.GetMessageLinkRequest{
		ChatId:     message.ChatId,
		MessageId:  message.Id,
		ForAlbum:   message.MediaAlbumId != 0,
		ForComment: false,
	})
	if err != nil {
		log.Print("GetMessageLink() ", err)
	} else {
		sourceLink, err := tdlibClient.ParseTextEntities(&client.ParseTextEntitiesRequest{
			Text: fmt.Sprintf("[%s%s](%s)", "\U0001f517", title, messageLink.Link),
			ParseMode: &client.TextParseModeMarkdown{
				Version: 2,
			},
		})
		if err != nil {
			log.Print("ParseTextEntities() ", err)
		} else {
			// TODO: тут упало на опросе https://t.me/Full_Time_Trading/40922
			offset := int32(len(utf16.Encode([]rune(formattedText.Text))))
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
	log.Printf("addSourceLink() %#v", formattedText)
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
		// 		log.Print("GetAttachedStickerSets() ", err)
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

func sendCopyNewMessages(tdlibClient *client.Client, messages []*client.Message, srcChatId, dscChatId int64, forward config.Forward) (*client.Messages, error) {
	// srcChatId - не использую, только для дебага
	contents := make([]client.InputMessageContent, 0)
	for i, message := range messages {
		if message.ForwardInfo != nil {
			if origin, ok := message.ForwardInfo.Origin.(*client.MessageForwardOriginChannel); ok {
				if originMessage, err := getOriginMessage(origin.ChatId, origin.MessageId); err != nil {
					log.Print("getOriginMessage() ", err)
				} else {
					messages[i] = originMessage
				}
			}
		}
		src := messages[i] // !!!! for origin message
		formattedText, contentMode := getFormattedText(src.Content)
		formattedText = copyFormattedText(formattedText)
		if replaceMyselfLink, ok := configData.ReplaceMyselfLinks[dscChatId]; ok {
			replaceMyselfLinks(formattedText, src.ChatId, dscChatId, replaceMyselfLink.DeleteExternal)
		}
		if i == 0 {
			if source, ok := configData.Sources[src.ChatId]; ok {
				if containsInt64(source.Sign.For, dscChatId) {
					addSourceSign(formattedText, source.Sign.Title)
				}
				if containsInt64(source.Link.For, dscChatId) {
					addSourceLink(src, formattedText, source.Link.Title)
				}
			}
		}
		content := getInputMessageContent(src.Content, formattedText, contentMode)
		if content != nil {
			contents = append(contents, content)
		}
	}
	if len(contents) == 1 {
		message, err := tdlibClient.SendMessage(&client.SendMessageRequest{
			ChatId:              dscChatId,
			InputMessageContent: contents[0],
		})
		if err != nil {
			return nil, err
		}
		return &client.Messages{
			TotalCount: 1,
			Messages:   []*client.Message{message},
		}, nil
	} else {
		return tdlibClient.SendMessageAlbum(&client.SendMessageAlbumRequest{
			ChatId:               dscChatId,
			InputMessageContents: contents,
		})
	}
}

func getOriginMessage(chatId, messageId int64) (*client.Message, error) {
	src, err := tdlibClient.GetMessage(&client.GetMessageRequest{
		ChatId:    chatId,
		MessageId: messageId,
	})
	if err != nil {
		return src, err
	}
	// рекурсия лишняя,
	// т.к. телега не пересылает пересланное сообщение, а сама подменяет на оригинальное;
	// но где гарантия, что кастомные клиенты работают так же? :)
	if src.ForwardInfo != nil {
		src, err = getOriginMessage(chatId, messageId)
	}
	return src, err
}

func replaceMyselfLinks(formattedText *client.FormattedText, srcChatId, dscChatId int64, withDeleteExternal bool) {
	log.Printf("replaceMyselfLinks() srcChatId: %d dscChatId: %d", srcChatId, dscChatId)
	for _, entity := range formattedText.Entities {
		if textUrl, ok := entity.Type.(*client.TextEntityTypeTextUrl); ok {
			if messageLinkInfo, err := tdlibClient.GetMessageLinkInfo(&client.GetMessageLinkInfoRequest{
				Url: textUrl.Url,
			}); err != nil {
				log.Print("GetMessageLinkInfo() ", err)
			} else {
				src := messageLinkInfo.Message
				if srcChatId == src.ChatId {
					isReplaced := false
					fromChatMessageId := fmt.Sprintf("%d:%d", src.ChatId, src.Id)
					toChatMessageIds := getCopiedMessageIds(fromChatMessageId)
					log.Printf("fromChatMessageId: %s toChatMessageIds: %v", fromChatMessageId, toChatMessageIds)
					var dscId int64 = 0
					for _, toChatMessageId := range toChatMessageIds {
						a := strings.Split(toChatMessageId, ":")
						if int64(convertToInt(a[0])) == dscChatId {
							dscId = int64(convertToInt(a[1]))
							break
						}
					}
					if dscId != 0 {
						if messageLink, err := tdlibClient.GetMessageLink(&client.GetMessageLinkRequest{
							ChatId:    dscChatId,
							MessageId: getNewMessageId(dscChatId, dscId),
						}); err != nil {
							log.Print("GetMessageLink() ", err)
						} else {
							entity.Type = &client.TextEntityTypeTextUrl{
								Url: messageLink.Link,
							}
							isReplaced = true
						}
					}
					if !isReplaced && withDeleteExternal {
						entity.Type = &client.TextEntityTypeStrikethrough{}
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
