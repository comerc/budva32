package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/comerc/budva32/accounts"
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

	if err := accounts.ReadConfigFile(); err != nil {
		fmt.Println("Can't initialise config:", err)
	}

	for _, config := range accounts.Configs {
		go run(config, apiId, apiHash)
	}

	for {
		time.Sleep(time.Hour)
	}
}

func run(config accounts.Config, apiId, apiHash string) {
	// client authorizer
	authorizer := client.ClientAuthorizer()
	go client.CliInteractor(authorizer)

	// or bot authorizer
	// botToken := "000000000:gsVCGG5YbikxYHC7bP5vRvmBqJ7Xz6vG6td"
	// authorizer := client.BotAuthorizer(botToken)

	authorizer.TdlibParameters <- &client.TdlibParameters{
		UseTestDc:              false,
		DatabaseDirectory:      filepath.Join("tddata", config.PhoneNumber+"-tdlib-db"),
		FilesDirectory:         filepath.Join("tddata", config.PhoneNumber+"-tdlib-files"),
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

	tdlibClient.SetLogStream(&client.SetLogStreamRequest{
		LogStream: &client.LogStreamFile{
			Path:           filepath.Join("tddata", config.PhoneNumber+"-errors.log"),
			MaxFileSize:    10485760,
			RedirectStderr: true,
		},
	})

	// Handle Ctrl+C
	// ch := make(chan os.Signal, 2)
	// signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	// go func() {
	// 	<-ch
	// 	tdlibClient.Stop()
	// 	os.Exit(1)
	// }()

	optionValue, err := tdlibClient.GetOption(&client.GetOptionRequest{
		Name: "version",
	})
	if err != nil {
		log.Fatalf("GetOption error: %s", err)
	}

	fmt.Printf("TDLib version: %s\n", optionValue.(*client.OptionValueString).Value)

	me, err := tdlibClient.GetMe()
	if err != nil {
		log.Fatalf("GetMe error: %s", err)
	}

	fmt.Printf("Me: %s %s [%s]\n", me.FirstName, me.LastName, me.Username)

	listener := tdlibClient.GetListener()
	defer listener.Close()

	for update := range listener.Updates {
		if update.GetClass() == client.ClassUpdate {
			updateNewMessage, ok := update.(*client.UpdateNewMessage)
			if ok {
				forwards := config.Forwards
				for _, forward := range forwards {
					if updateNewMessage.Message.ChatId == forward.From {
						fmt.Println(config.PhoneNumber, "- Message ", updateNewMessage.Message.Id, " forwarded from ", updateNewMessage.Message.ChatId)
						for _, to := range forward.To {
							formattedText := updateNewMessage.Message.Content.(*client.MessageText).Text
							inputMessageContent := client.InputMessageText{
								Text:                  formattedText,
								DisableWebPagePreview: true,
								ClearDraft:            true,
							}
							message, err := tdlibClient.SendMessage(&client.SendMessageRequest{
								ChatId:              to,
								InputMessageContent: &inputMessageContent,
							})
							if err != nil {
								fmt.Println(err)
							} else {
								fmt.Println(">>>>")
								fmt.Printf("%#v\n", message)
							}
						}
					}
				}
			}
		}
	}
}

func convertToInt32(s string) int32 {
	i, err := strconv.Atoi(s)
	if err != nil {
		fmt.Println(err)
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
