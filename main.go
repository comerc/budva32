package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/comerc/budva32/account"
	"github.com/joho/godotenv"
	"github.com/zelenin/go-tdlib/client"
)

func main() {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatalf("Error loading .env file")
	}
	var (
		apiId   = os.Getenv("BUDVA32_API_ID")
		apiHash = os.Getenv("BUDVA32_API_HASH")
	)

	if err := account.ReadConfigFile(); err != nil {
		log.Fatalf("Can't initialise config: %s", err)
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
		UseFileDatabase:        true,
		UseChatInfoDatabase:    true,
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
	// defer tdlibClient.Stop()

	tdlibClient.SetLogStream(&client.SetLogStreamRequest{
		LogStream: &client.LogStreamFile{
			Path:           filepath.Join("tddata", account.Config.PhoneNumber+"-errors.log"),
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

	log.Printf("Me: %s %s [%s]", me.FirstName, me.LastName, me.Username)

	listener := tdlibClient.GetListener()
	// defer listener.Close()

	forwards := account.Config.Forwards
	for update := range listener.Updates {
		if update.GetClass() == client.ClassUpdate {
			// TODO: how to copy Album (via SendMessageAlbum)
			if updateMessageEdited, ok := update.(*client.UpdateMessageEdited); ok {
				src := getMessage(tdlibClient,
					updateMessageEdited.ChatId,
					updateMessageEdited.MessageId,
				)
				for _, forward := range forwards {
					if src.ChatId == forward.From {
						for _, dscChatId := range forward.To {
							forwardMessageEdited(tdlibClient, src, dscChatId)
						}
					}
				}
			} else if updateNewMessage, ok := update.(*client.UpdateNewMessage); ok {
				src := updateNewMessage.Message
				for _, forward := range forwards {
					if src.ChatId == forward.From {
						for _, dscChatId := range forward.To {
							forwardNewMessage(tdlibClient, src, dscChatId)
						}
					}
				}
			}
		}
	}

	// Handle Ctrl+C
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		listener.Close()
		tdlibClient.Stop()
		os.Exit(1)
	}()

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

func forwardMessageEdited(tdlibClient *client.Client, src *client.Message, dscChatId int64) {
	var formattedText *client.FormattedText
	if content, ok := src.Content.(*client.MessageText); ok {
		formattedText = content.Text
	} else if content, ok := src.Content.(*client.MessagePhoto); ok {
		formattedText = content.Caption
	} else if content, ok := src.Content.(*client.MessageAnimation); ok {
		formattedText = content.Caption
	} else if content, ok := src.Content.(*client.MessageAudio); ok {
		formattedText = content.Caption
	} else if content, ok := src.Content.(*client.MessageDocument); ok {
		formattedText = content.Caption
	} else if content, ok := src.Content.(*client.MessageVideo); ok {
		formattedText = content.Caption
	} else if content, ok := src.Content.(*client.MessageVoiceNote); ok {
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
		forwardNewMessage(tdlibClient, src, dscChatId)
		return
	}
	dsc, err := tdlibClient.SendMessage(&client.SendMessageRequest{
		ChatId: dscChatId,
		InputMessageContent: &client.InputMessageText{
			Text:                  formattedText,
			DisableWebPagePreview: true,
			ClearDraft:            true,
		},
		ReplyToMessageId: getMessageId(src.ChatId, src.Id, dscChatId),
	})
	if err != nil {
		log.Print(err)
	} else {
		setMessageId(src.ChatId, src.Id, dsc.ChatId, dsc.Id)
	}
}

func getMessage(tdlibClient *client.Client, ChatId, Id int64) *client.Message {
	result, err := tdlibClient.GetMessage(&client.GetMessageRequest{
		ChatId:    ChatId,
		MessageId: Id,
	})
	if err != nil {
		log.Print(err)
	}
	return result
}

func forwardNewMessage(tdlibClient *client.Client, src *client.Message, dscChatId int64) {
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
		SendCopy:      true,
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
