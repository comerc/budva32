package main

import (
	"bytes"
	"crypto/subtle"
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
	"github.com/joho/godotenv"
	"github.com/zelenin/go-tdlib/client"
)

// TODO: подменять ссылки внутри сообщений на группу / канал (если копируется всё полностью)
// TODO: badger
// TODO: копировать закреп сообщений

const (
	projectName = "budva32"
)

var (
	inputCh       = make(chan string, 1)
	outputCh      = make(chan string, 1)
	forwards      []config.Forward
	tdlibClient   *client.Client
	mediaAlbumsMu sync.Mutex
	forwardsMu    sync.Mutex
)

func main() {
	var err error

	if err = godotenv.Load(); err != nil {
		log.Fatalf("Error loading .env file")
	}
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
		}
		forwardsMu.Lock()
		defer forwardsMu.Unlock()
		forwards = tmp
	})

	go func() {
		http.HandleFunc("/favicon.ico", getFaviconHandler)
		http.HandleFunc("/", withBasicAuth(withAuthentiation(getChatsHandler)))
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

	path := filepath.Join(".", "tdata")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.Mkdir(path, os.ModePerm)
	}

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

	for update := range listener.Updates {
		if update.GetClass() == client.ClassUpdate {
			if updateNewMessage, ok := update.(*client.UpdateNewMessage); ok {
				for _, forward := range getForwards() {
					src := updateNewMessage.Message
					if src.ChatId == forward.From && src.CanBeForwarded {
						if src.MediaAlbumId == 0 {
							time.Sleep(3 * time.Second) // иначе бот не успевает вставить своё
							doUpdateNewMessage([]*client.Message{src}, forward)
						} else {
							isFirstMessage := addMessageToMediaAlbum(src)
							if isFirstMessage {
								forward := forward // !!!
								go handleMediaAlbum(src.MediaAlbumId,
									func(messages []*client.Message) {
										doUpdateNewMessage(messages, forward)
									})
							}
						}
					}
				}
			} else if updateMessageEdited, ok := update.(*client.UpdateMessageEdited); ok {
				isSetSrc := false
				var formattedText *client.FormattedText
				var contentMode ContentMode
				search := ChatMessageId(fmt.Sprintf("%d:%d", updateMessageEdited.ChatId, updateMessageEdited.MessageId))
				for to, from := range copiedMessageIds {
					if from != search {
						continue
					}
					if !isSetSrc {
						isSetSrc = true
						src, err := tdlibClient.GetMessage(&client.GetMessageRequest{
							ChatId:    updateMessageEdited.ChatId,
							MessageId: updateMessageEdited.MessageId,
						})
						if err != nil {
							log.Print(err)
							break
						}
						formattedText, contentMode = getFormattedText(src.Content)
					}
					a := strings.Split(string(to), ":")
					dscChatId := int64(convertToInt(a[0]))
					dscId := int64(convertToInt(a[1]))
					newMessageId := getNewMessageId(dscChatId, dscId)
					{
						dsc, err := tdlibClient.GetMessage(&client.GetMessageRequest{
							ChatId:    dscChatId,
							MessageId: newMessageId,
						})
						if err != nil {
							log.Print(err)
							continue
						}
						dscFormattedText, _ := getFormattedText(dsc.Content)
						srcFormattedText := formattedText
						isEqualFormattedText := false
						if srcFormattedText == nil && dscFormattedText == nil {
							isEqualFormattedText = true
						} else if srcFormattedText != nil && dscFormattedText == nil || srcFormattedText == nil && dscFormattedText != nil {
							isEqualFormattedText = false
						} else if srcFormattedText.Text != dscFormattedText.Text {
							isEqualFormattedText = false
						} else if len(srcFormattedText.Entities) != len(dscFormattedText.Entities) {
							isEqualFormattedText = false
						} else {
							for i, srcEntity := range srcFormattedText.Entities {
								dscEntity := dscFormattedText.Entities[i]
								if srcEntity.Offset == dscEntity.Offset &&
									srcEntity.Length == dscEntity.Length &&
									srcEntity.Type == dscEntity.Type {
									var (
										err     error
										srcJSON []byte
										dscJSON []byte
									)
									srcJSON, err = srcEntity.MarshalJSON()
									if err != nil {
										break
									}
									dscJSON, err = dscEntity.MarshalJSON()
									if err != nil {
										break
									}
									if bytes.Equal(srcJSON, dscJSON) {
										isEqualFormattedText = true
									}
									break
								}
							}
						}
						// если formattedText не изменился (когда кнопки нажимают)
						if isEqualFormattedText {
							continue
						}
					}
					switch contentMode {
					case ContentModeText:
						dsc, err := tdlibClient.EditMessageText(&client.EditMessageTextRequest{
							ChatId:    dscChatId,
							MessageId: newMessageId,
							InputMessageContent: &client.InputMessageText{
								Text:                  formattedText,
								DisableWebPagePreview: true,
								ClearDraft:            true,
							},
						})
						if err != nil {
							log.Print(err)
						}
						_ = dsc // TODO: log
					case ContentModeCaption:
						dsc, err := tdlibClient.EditMessageCaption(&client.EditMessageCaptionRequest{
							ChatId:    dscChatId,
							MessageId: newMessageId,
							Caption:   formattedText,
						})
						if err != nil {
							log.Print(err)
						}
						_ = dsc // TODO: log
					}
				}
				// for _, forward := range getForwards() {
				// 	src := updateMessageEdited
				// 	if src.ChatId == forward.From && forward.WithEdited {
				// 		src, err := tdlibClient.GetMessage(&client.GetMessageRequest{
				// 			ChatId:    src.ChatId,
				// 			MessageId: src.MessageId,
				// 		})
				// 		if err != nil {
				// 			log.Print(err)
				// 			continue
				// 		}
				// 		doUpdateMessageEdited(src, forward)
				// 	}
				// }
			} else if updateMessageSendSucceeded, ok := update.(*client.UpdateMessageSendSucceeded); ok {
				message := updateMessageSendSucceeded.Message
				setNewMessageId(message.ChatId, updateMessageSendSucceeded.OldMessageId, message.Id)
			}
		}
	}
}

func convertToInt(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		log.Print(err)
		return 0
	}
	return int(i)
}

// ****

type ChatMessageId string // chatId:messageId

var copiedMessageIds = make(map[ChatMessageId]ChatMessageId) // [To]From

var newMessageIds = make(map[ChatMessageId]int64)

func setNewMessageId(chatId, oldId, newId int64) {
	newMessageIds[ChatMessageId(fmt.Sprintf("%d:%d", chatId, oldId))] = newId
}

func getNewMessageId(chatId, oldId int64) int64 {
	return newMessageIds[ChatMessageId(fmt.Sprintf("%d:%d", chatId, oldId))]
}

// func getEditedLabel(isEdited bool) string {
// 	if isEdited {
// 		return " EDITED!"
// 	}
// 	return ""
// }
// formattedText.Text = fmt.Sprintf("%s\n\n#C%dM%d%s",
// 	formattedText.Text, -src.ChatId, src.Id, getEditedLabel(isEdited))

// func forwardMessageEdited(tdlibClient *client.Client, formattedText *client.FormattedText, srcChatId, srcId, dscChatId int64) {
// 	dsc, err := tdlibClient.SendMessage(&client.SendMessageRequest{
// 		ChatId: dscChatId,
// 		InputMessageContent: &client.InputMessageText{
// 			Text:                  formattedText,
// 			DisableWebPagePreview: true,
// 			ClearDraft:            true,
// 		},
// 		ReplyToMessageId: getMessageId(srcChatId, srcId, dscChatId),
// 	})
// 	if err != nil {
// 		log.Print(err)
// 	} else {
// 		setMessageId(srcChatId, srcId, dsc.ChatId, dsc.Id)
// 	}
// }

// func forwardMessageAlbumEdited(tdlibClient *client.Client, messages []*client.Message, srcChatId, srcId, dscChatId int64) {
// 	dsc, err := tdlibClient.SendMessageAlbum(&client.SendMessageAlbumRequest{
// 		InputMessageContents: func() []client.InputMessageContent {
// 			result := make([]client.InputMessageContent, 0)
// 			for _, message := range messages {
// 				src := message
// 				messageContent := src.Content
// 				messagePhoto := messageContent.(*client.MessagePhoto)
// 				result = append(result, &client.InputMessagePhoto{
// 					Photo: &client.InputFileRemote{
// 						Id: string(messagePhoto.Photo.Sizes[0].Photo.Id),
// 					},
// 					Caption: messagePhoto.Caption,
// 				})
// 			}
// 			return result
// 		}(),
// 		ReplyToMessageId: getMessageId(srcChatId, srcId, dscChatId),
// 	})
// 	if err != nil {
// 		log.Print(err)
// 	} else {
// 		dsc := dsc.Messages[0]
// 		setMessageId(srcChatId, srcId, dsc.ChatId, dsc.Id)
// 	}
// }

func forwardNewMessages(tdlibClient *client.Client, messages []*client.Message, srcChatId, dscChatId int64, forward config.Forward) {
	var messageIds []int64
	for _, message := range messages {
		messageIds = append(messageIds, message.Id)
	}
	forwardedMessages, err := tdlibClient.ForwardMessages(&client.ForwardMessagesRequest{
		ChatId:     dscChatId,
		FromChatId: srcChatId,
		MessageIds: messageIds,
		Options: &client.MessageSendOptions{
			DisableNotification: false,
			FromBackground:      false,
			SchedulingState: &client.MessageSchedulingStateSendAtDate{
				SendDate: int32(time.Now().Unix()),
			},
		},
		SendCopy:      forward.SendCopy,
		RemoveCaption: false,
	})
	if err != nil {
		log.Print(err)
	} else if len(forwardedMessages.Messages) != int(forwardedMessages.TotalCount) || forwardedMessages.TotalCount == 0 {
		log.Print("Invalid TotalCount")
	} else if forward.SendCopy {
		for i, dsc := range forwardedMessages.Messages {
			srcId := messageIds[i]
			dscId := dsc.Id
			to := ChatMessageId(fmt.Sprintf("%d:%d", dscChatId, dscId))
			from := ChatMessageId(fmt.Sprintf("%d:%d", srcChatId, srcId))
			copiedMessageIds[to] = from
		}
	}
}

type ContentMode string

const (
	ContentModeText    = "text"
	ContentModeCaption = "caption"
)

func getFormattedText(messageContent client.MessageContent) (*client.FormattedText, ContentMode) {
	var (
		formattedText *client.FormattedText
		contentMode   ContentMode
	)
	if content, ok := messageContent.(*client.MessageText); ok {
		formattedText = content.Text
		contentMode = ContentModeText
	} else if content, ok := messageContent.(*client.MessagePhoto); ok {
		formattedText = content.Caption
		contentMode = ContentModeCaption
	} else if content, ok := messageContent.(*client.MessageAnimation); ok {
		formattedText = content.Caption
		contentMode = ContentModeCaption
	} else if content, ok := messageContent.(*client.MessageAudio); ok {
		formattedText = content.Caption
		contentMode = ContentModeCaption
	} else if content, ok := messageContent.(*client.MessageDocument); ok {
		formattedText = content.Caption
		contentMode = ContentModeCaption
	} else if content, ok := messageContent.(*client.MessageVideo); ok {
		formattedText = content.Caption
		contentMode = ContentModeCaption
	} else if content, ok := messageContent.(*client.MessageVoiceNote); ok {
		formattedText = content.Caption
		contentMode = ContentModeCaption
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
		formattedText = nil
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

func checkFilters(formattedText *client.FormattedText, forward config.Forward, isOther *bool) bool {
	*isOther = false
	if formattedText == nil {
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

var mediaAlbums = make(map[client.JsonInt64]MediaAlbum)

// https://github.com/tdlib/td/issues/1482
func addMessageToMediaAlbum(message *client.Message) bool {
	item, ok := mediaAlbums[message.MediaAlbumId]
	if !ok {
		item = MediaAlbum{}
	}
	item.messages = append(item.messages, message)
	item.lastReceived = time.Now()
	mediaAlbums[message.MediaAlbumId] = item
	return !ok
}

func getMediaAlbumLastReceivedDiff(id client.JsonInt64) time.Duration {
	mediaAlbumsMu.Lock()
	defer mediaAlbumsMu.Unlock()
	return time.Since(mediaAlbums[id].lastReceived)
}

func getMediaAlbumMessages(id client.JsonInt64) []*client.Message {
	mediaAlbumsMu.Lock()
	defer mediaAlbumsMu.Unlock()
	messages := mediaAlbums[id].messages
	delete(mediaAlbums, id)
	return messages
}

func handleMediaAlbum(id client.JsonInt64, cb func(messages []*client.Message)) {
	diff := getMediaAlbumLastReceivedDiff(id)
	const pause = 3 * time.Second
	if diff < pause {
		time.Sleep(pause)
		handleMediaAlbum(id, cb)
		return
	}
	messages := getMediaAlbumMessages(id)
	cb(messages)
}

func doUpdateNewMessage(messages []*client.Message, forward config.Forward) {
	src := messages[0]
	formattedText, _ := getFormattedText(src.Content)
	log.Printf("updateNewMessage go ChatId: %d Id: %d hasText: %t MediaAlbumId: %d", src.ChatId, src.Id, formattedText != nil && formattedText.Text != "", src.MediaAlbumId)
	isFilters := false
	isOther := false
	var forwardedTo []int64
	if checkFilters(formattedText, forward, &isOther) {
		isFilters = true
		for _, dscChatId := range forward.To {
			forwardNewMessages(tdlibClient, messages, src.ChatId, dscChatId, forward)
			forwardedTo = append(forwardedTo, dscChatId)
		}
	} else if isOther && forward.Other != 0 {
		dscChatId := forward.Other
		forwardNewMessages(tdlibClient, messages, src.ChatId, dscChatId, forward)
		forwardedTo = append(forwardedTo, dscChatId)
		if forward.SendCopy && forward.SourceTitle != "" {
			messageLink, err := tdlibClient.GetMessageLink(&client.GetMessageLinkRequest{
				ChatId:     src.ChatId,
				MessageId:  src.Id,
				ForAlbum:   src.MediaAlbumId != 0,
				ForComment: false,
			})
			if err != nil {
				log.Print(err)
			} else if !messageLink.IsPublic {
				log.Print("Invalid messageLink.IsPublic for ChatId:", src.ChatId)
			} else {
				text := forward.SourceTitle
				boldEntity := &client.TextEntity{
					Offset: 0,
					Length: int32(len([]rune(text))),
					Type:   &client.TextEntityTypeBold{},
				}
				urlEntity := &client.TextEntity{
					Offset: 0,
					Length: int32(len([]rune(text))),
					Type: &client.TextEntityTypeTextUrl{
						Url: messageLink.Link,
					},
				}
				_, err := tdlibClient.SendMessage(&client.SendMessageRequest{
					ChatId: dscChatId,
					InputMessageContent: &client.InputMessageText{
						Text: &client.FormattedText{
							Text:     text,
							Entities: []*client.TextEntity{boldEntity, urlEntity},
						},
						DisableWebPagePreview: true,
						ClearDraft:            true,
					},
				})
				if err != nil {
					log.Print(err)
				}
			}
		}
	}
	log.Printf("updateNewMessage ok isFilters: %t isOther: %t forwardedTo: %v", isFilters, isOther, forwardedTo)
}

// func doUpdateMessageEdited(src *client.Message, forward config.Forward) {
// 	formattedText := getFormattedText(src.Content)
// 	log.Printf("updateMessageEdited go ChatId: %d Id: %d hasText: %t MediaAlbumId: %d", src.ChatId, src.Id, formattedText != nil && formattedText.Text != "", src.MediaAlbumId)
// 	isFilters := false
// 	isOther := false
// 	var forwardedTo []int64
// 	if checkFilters(formattedText, forward, &isOther) {
// 		isFilters = true
// 		for _, dscChatId := range forward.To {
// 			if formattedText == nil {
// 				forwardNewMessages(tdlibClient, []*client.Message{src}, dscChatId, forward.SendCopy)
// 				// TODO: ещё одно сообщение со ссылкой на исходник редактирования
// 			} else {
// 				forwardMessageEdited(tdlibClient, formattedText, src.ChatId, src.Id, dscChatId)
// 			}
// 			forwardedTo = append(forwardedTo, dscChatId)
// 		}
// 	} else if isOther && forward.Other != 0 {
// 		dscChatId := forward.Other
// 		if formattedText == nil {
// 			forwardNewMessages(tdlibClient, []*client.Message{src}, dscChatId, forward.SendCopy)
// 			// TODO: ещё одно сообщение со ссылкой на исходник редактирования
// 		} else {
// 			forwardMessageEdited(tdlibClient, formattedText, src.ChatId, src.Id, dscChatId)
// 		}
// 		forwardedTo = append(forwardedTo, dscChatId)
// 	}
// 	log.Printf("updateMessageEdited ok isFilters: %t isOther: %t forwardedTo: %v", isFilters, isOther, forwardedTo)
// 	// log.Printf("updateMessageEdited ok forwardedTo: %v", forwardedTo)
// }

func getForwards() []config.Forward {
	forwardsMu.Lock()
	defer forwardsMu.Unlock()
	result := forwards // ???
	return result
}

func handlePanic() {
	if err := recover(); err != nil {
		log.Printf("Panic...\n%s\n\n%s", err, debug.Stack())
		os.Exit(1)
	}
}
