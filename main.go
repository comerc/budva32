package main

import (
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
	"strconv"
	"syscall"
	"time"

	config "github.com/comerc/budva32/config"
	"github.com/joho/godotenv"
	"github.com/zelenin/go-tdlib/client"
)

// TODO: кнопки под сообщением генерируют UpdateMessageEdited,
// TODO: пускай бот segezha4 отвечает только на новые сообщения?
// TODO: Matches
// TODO: how to copy Album (via SendMessageAlbum)

// TODO: сообщения обновляются из-за прикрепленных кнопок
// TODO: reload & edit config.yml via web

func main() {
	// Handle Ctrl+C
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		log.Print("Stop...")
		os.Exit(1)
	}()

	if err := godotenv.Load(); err != nil {
		log.Fatalf("Error loading .env file")
	}
	var (
		apiId   = os.Getenv("BUDVA32_API_ID")
		apiHash = os.Getenv("BUDVA32_API_HASH")
		port    = os.Getenv("BUDVA32_PORT")
	)

	if err := config.Load(); err != nil {
		log.Fatalf("Can't initialise config: %s", err)
	}
	config := config.GetConfig()
	forwards := config.Forwards
	for _, forward := range forwards {
		for _, dscChatId := range forward.To {
			if forward.From == dscChatId {
				log.Fatalf("Invalid config. Destination Id cannot be equal source Id: %d", dscChatId)
			}
		}
	}

	// client authorizer
	authorizer := client.ClientAuthorizer()
	go client.CliInteractor(authorizer)

	// or bot authorizer
	// botToken := "000000000:gsVCGG5YbikxYHC7bP5vRvmBqJ7Xz6vG6td"
	// authorizer := client.BotAuthorizer(botToken)

	authorizer.TdlibParameters <- &client.TdlibParameters{
		UseTestDc:              false,
		DatabaseDirectory:      filepath.Join("tdata", "db"),
		FilesDirectory:         filepath.Join("tdata", "files"),
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
				Path:           filepath.Join("tdata", ".log"),
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

	tdlibClient, err := client.NewClient(authorizer, logStream, logVerbosity)
	if err != nil {
		log.Fatalf("NewClient error: %s", err)
	}
	defer tdlibClient.Stop()

	optionValue, err := tdlibClient.GetOption(&client.GetOptionRequest{
		Name: "version",
	})
	if err != nil {
		log.Fatalf("GetOption error: %s", err)
	}

	log.Printf("TDLib version: %s", optionValue.(*client.OptionValueString).Value)

	me, err := tdlibClient.GetMe()
	if err != nil {
		log.Fatalf("GetMe error: %s", err)
	}

	log.Printf("Me: %s %s [@%s]", me.FirstName, me.LastName, me.Username)

	go func() {
		http.HandleFunc("/favicon.ico", getFaviconHandler)
		http.HandleFunc("/", withBasicAuth(getChatsHandler(tdlibClient)))
		host := getIP()
		port := ":" + port
		fmt.Println("Web-server is running: http://" + host + port)
		if err := http.ListenAndServe(port, http.DefaultServeMux); err != nil {
			log.Fatal("Error starting http server: ", err)
			return
		}
	}()

	listener := tdlibClient.GetListener()
	defer listener.Close()

	for update := range listener.Updates {
		if update.GetClass() == client.ClassUpdate {
			if updateNewMessage, ok := update.(*client.UpdateNewMessage); ok {
				for _, forward := range forwards {
					src := updateNewMessage.Message
					if src.ChatId == forward.From {
						formattedText := getFormattedText(src.Content)
						isOther := false
						if canSend(formattedText, &forward, &isOther) {
							for _, dscChatId := range forward.To {
								forwardNewMessage(tdlibClient, src, dscChatId, forward.SendCopy)
							}
						} else if isOther && forward.Other != 0 {
							dscChatId := forward.Other
							forwardNewMessage(tdlibClient, src, dscChatId, forward.SendCopy)
						}
					}
				}
			} else if updateMessageEdited, ok := update.(*client.UpdateMessageEdited); ok {
				for _, forward := range forwards {
					src := updateMessageEdited
					if src.ChatId == forward.From && forward.WithEdited {
						src, err := tdlibClient.GetMessage(&client.GetMessageRequest{
							ChatId:    src.ChatId,
							MessageId: src.MessageId,
						})
						if err != nil {
							log.Print(err)
							continue
						}
						formattedText := getFormattedText(src.Content)
						isOther := false
						if canSend(formattedText, &forward, &isOther) {
							for _, dscChatId := range forward.To {
								if formattedText == nil {
									forwardNewMessage(tdlibClient, src, dscChatId, forward.SendCopy)
									// TODO: ещё одно сообщение со ссылкой на исходник редактирования
								} else {
									forwardMessageEdited(tdlibClient, formattedText, src.ChatId, src.Id, dscChatId)
								}
							}
						} else if isOther && forward.Other != 0 {
							dscChatId := forward.Other
							if formattedText == nil {
								forwardNewMessage(tdlibClient, src, dscChatId, forward.SendCopy)
								// TODO: ещё одно сообщение со ссылкой на исходник редактирования
							} else {
								forwardMessageEdited(tdlibClient, formattedText, src.ChatId, src.Id, dscChatId)
							}
						}
					}
				}
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

var messageIds = make(map[string]int64)

func setMessageId(srcChatId, srcId, dscChatId, dscId int64) {
	messageIds[fmt.Sprintf("%d:%d:%d", srcChatId, srcId, dscChatId)] = dscId
}

func getMessageId(srcChatId, srcId, dscChatId int64) int64 {
	return messageIds[fmt.Sprintf("%d:%d:%d", srcChatId, srcId, dscChatId)]
}

// func getEditedLabel(isEdited bool) string {
// 	if isEdited {
// 		return " EDITED!"
// 	}
// 	return ""
// }
// formattedText.Text = fmt.Sprintf("%s\n\n#C%dM%d%s",
// 	formattedText.Text, -src.ChatId, src.Id, getEditedLabel(isEdited))

func forwardMessageEdited(tdlibClient *client.Client, formattedText *client.FormattedText, srcChatId, srcId, dscChatId int64) {
	dsc, err := tdlibClient.SendMessage(&client.SendMessageRequest{
		ChatId: dscChatId,
		InputMessageContent: &client.InputMessageText{
			Text:                  formattedText,
			DisableWebPagePreview: true,
			ClearDraft:            true,
		},
		ReplyToMessageId: getMessageId(srcChatId, srcId, dscChatId),
	})
	if err != nil {
		log.Print(err)
	} else {
		setMessageId(srcChatId, srcId, dsc.ChatId, dsc.Id)
	}
}

func forwardNewMessage(tdlibClient *client.Client, src *client.Message, dscChatId int64, SendCopy bool) {
	forwardedMessages, err := tdlibClient.ForwardMessages(&client.ForwardMessagesRequest{
		ChatId:     dscChatId,
		FromChatId: src.ChatId,
		MessageIds: []int64{src.Id},
		Options: &client.MessageSendOptions{
			DisableNotification: false,
			FromBackground:      false,
			SchedulingState: &client.MessageSchedulingStateSendAtDate{
				SendDate: int32(time.Now().Unix()),
			},
		},
		SendCopy:      SendCopy,
		RemoveCaption: false,
	})
	if err != nil {
		log.Print(err)
	} else if forwardedMessages.TotalCount != 1 {
		log.Print("Invalid TotalCount")
	} else {
		dsc := forwardedMessages.Messages[0]
		setMessageId(src.ChatId, src.Id, dsc.ChatId, dsc.Id)
	}
}

func getFormattedText(messageContent client.MessageContent) *client.FormattedText {
	var formattedText *client.FormattedText
	if content, ok := messageContent.(*client.MessageText); ok {
		formattedText = content.Text
	} else if content, ok := messageContent.(*client.MessagePhoto); ok {
		formattedText = content.Caption
	} else if content, ok := messageContent.(*client.MessageAnimation); ok {
		formattedText = content.Caption
	} else if content, ok := messageContent.(*client.MessageAudio); ok {
		formattedText = content.Caption
	} else if content, ok := messageContent.(*client.MessageDocument); ok {
		formattedText = content.Caption
	} else if content, ok := messageContent.(*client.MessageVideo); ok {
		formattedText = content.Caption
	} else if content, ok := messageContent.(*client.MessageVoiceNote); ok {
		formattedText = content.Caption
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
		return nil
	}
	return formattedText
}

func contains(a []string, s string) bool {
	for _, t := range a {
		if t == s {
			return true
		}
	}
	return false
}

func canSend(formattedText *client.FormattedText, forward *config.Forward, isOther *bool) bool {
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

func getChatsHandler(tdlibClient *client.Client) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
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
