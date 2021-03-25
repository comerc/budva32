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

	for update := range listener.Updates {
		if update.GetClass() == client.ClassUpdate {
			updateMessageEdited, ok := update.(*client.UpdateMessageEdited)
			if ok {
				src := getMessage(tdlibClient,
					updateMessageEdited.ChatId,
					updateMessageEdited.MessageId,
				)
				forwardMessage(tdlibClient, src, true)
			}
			updateNewMessage, ok := update.(*client.UpdateNewMessage)
			if ok {
				src := updateNewMessage.Message
				forwardMessage(tdlibClient, src, false)
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

// var messageIds = make(map[string]int64)
//
// func setMessageId(srcChatId, srcMessageId, dscChatId, dscMessageId int64) {
// 	messageIds[fmt.Sprintf("%d:%d:%d", srcChatId, srcMessageId, dscChatId)] = dscMessageId
// }
//
// func getMessageId(srcChatId, srcMessageId, dscChatId int64) int64 {
// 	return messageIds[fmt.Sprintf("%d:%d:%d", srcChatId, srcMessageId, dscChatId)]
// }

func getEditedLabel(isEdited bool) string {
	if isEdited {
		return " EDITED!"
	}
	return ""
}

func forwardMessage(tdlibClient *client.Client, src *client.Message, isEdited bool) {
	forwards := account.Config.Forwards
	for _, forward := range forwards {
		if src.ChatId == forward.From {
			for _, to := range forward.To {
				formattedText := src.Content.(*client.MessageText).Text
				formattedText.Text = fmt.Sprintf("%s\n\n#C%dM%d%s",
					formattedText.Text, -src.ChatId, src.Id, getEditedLabel(isEdited))
				inputMessageContent := client.InputMessageText{
					Text:                  formattedText,
					DisableWebPagePreview: true,
					ClearDraft:            true,
				}
				dsc, err := tdlibClient.SendMessage(&client.SendMessageRequest{
					ChatId:              to,
					InputMessageContent: &inputMessageContent,
				})
				if err != nil {
					log.Print(err)
					_ = dsc
				}
			}
		}
	}
}

func getMessage(tdlibClient *client.Client, ChatId, MessageId int64) *client.Message {
	result, err := tdlibClient.GetMessage(&client.GetMessageRequest{
		ChatId:    ChatId,
		MessageId: MessageId,
	})
	if err != nil {
		log.Print(err)
	}
	return result
}
