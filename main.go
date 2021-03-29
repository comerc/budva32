package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/comerc/budva32/account"
	"github.com/joho/godotenv"
	"github.com/zelenin/go-tdlib/client"
)

// TODO: упаковать в Docker (для старой Ubuntu)
// TODO: статический tdlib
// TODO: badger
// TODO: вкурить go-каналы
// TODO: как очищать message database tdlib

func main() {
	var chatListLimit int
	flag.IntVar(&chatListLimit, "chatlist", 0, "Get chat list with limit")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Fatalf("Error loading .env file")
	}
	var (
		apiId   = os.Getenv("BUDVA32_API_ID")
		apiHash = os.Getenv("BUDVA32_API_HASH")
	)

	if err := account.ReadConfigFile(); err != nil {
		log.Fatalf("Can't initialise config: %s", err)
	}
	forwards := account.Config.Forwards
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
		DatabaseDirectory:      filepath.Join("tddata", account.Config.PhoneNumber+"-tdlib-db"),
		FilesDirectory:         filepath.Join("tddata", account.Config.PhoneNumber+"-tdlib-files"),
		UseFileDatabase:        false,
		UseChatInfoDatabase:    false,
		UseMessageDatabase:     true,
		UseSecretChats:         false,
		ApiId:                  convertToInt32(apiId),
		ApiHash:                apiHash,
		SystemLanguageCode:     "en",
		DeviceModel:            "Server",
		SystemVersion:          "1.0.0",
		ApplicationVersion:     "1.0.0",
		EnableStorageOptimizer: true,
		IgnoreFileNames:        false,
	}

	logVerbosity := client.WithLogVerbosity(&client.SetLogVerbosityLevelRequest{
		NewVerbosityLevel: 1,
	})

	tdlibClient, err := client.NewClient(authorizer, logVerbosity)
	if err != nil {
		log.Fatalf("NewClient error: %s", err)
	}
	defer tdlibClient.Stop()

	tdlibClient.SetLogStream(&client.SetLogStreamRequest{
		LogStream: &client.LogStreamFile{
			Path:           filepath.Join("tddata", account.Config.PhoneNumber+".log"),
			MaxFileSize:    10485760,
			RedirectStderr: true,
		},
	})

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

	if chatListLimit > 0 {
		chats, err := tdlibClient.GetChats(&client.GetChatsRequest{
			ChatList:     &client.ChatListMain{},
			Limit:        int32(chatListLimit),
			OffsetOrder:  client.JsonInt64(int64(math.MaxInt64)),
			OffsetChatId: int64(0),
		})
		if err != nil {
			log.Fatalf("GetChats error: %s", err)
		}
		for _, chatId := range chats.ChatIds {
			chat, err := tdlibClient.GetChat(&client.GetChatRequest{
				ChatId: chatId,
			})
			if err != nil {
				log.Fatalf("GetChat error: %s", err)
			}
			fmt.Println(chat.Id, chat.Title)
		}
		os.Exit(1)
		return
	}

	listener := tdlibClient.GetListener()
	defer listener.Close()

	for update := range listener.Updates {
		if update.GetClass() == client.ClassUpdate {
			// TODO: how to copy Album (via SendMessageAlbum)
			if updateNewMessage, ok := update.(*client.UpdateNewMessage); ok {
				src := updateNewMessage.Message
				formattedText := getFormattedText(src.Content)
				for _, forward := range forwards {
					if src.ChatId == forward.From && canSend(formattedText, &forward) {
						for _, dscChatId := range forward.To {
							// fmt.Println("forwardNewMessage:", forward.SendCopy)
							forwardNewMessage(tdlibClient, src, dscChatId, forward.SendCopy)
						}
					}
				}
			} else if updateMessageEdited, ok := update.(*client.UpdateMessageEdited); ok {
				src, err := tdlibClient.GetMessage(&client.GetMessageRequest{
					ChatId:    updateMessageEdited.ChatId,
					MessageId: updateMessageEdited.MessageId,
				})
				if err != nil {
					log.Print(err)
					continue
				}
				formattedText := getFormattedText(src.Content)
				for _, forward := range forwards {
					if src.ChatId == forward.From && canSend(formattedText, &forward) {
						for _, dscChatId := range forward.To {
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

	// Handle Ctrl+C
	// ch := make(chan os.Signal, 2)
	// signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	// go func() {
	// 	<-ch
	// 	os.Exit(1)
	// }()

	for {
		time.Sleep(time.Hour)
	}
}

func convertToInt32(s string) int32 {
	i, err := strconv.Atoi(s)
	if err != nil {
		log.Print(err)
		return 0
	}
	return int32(i)
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

func canSend(formattedText *client.FormattedText, forward *account.Forward) bool {
	if formattedText != nil {
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
			return false
		}
	}
	return true
}
