package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/comerc/budva32/accounts"
	"github.com/joho/godotenv"
	"github.com/zelenin/go-tdlib/client"
)

func main() {

	// client authorizer
	authorizer := client.ClientAuthorizer()
	go client.CliInteractor(authorizer)

	// or bot authorizer
	// botToken := "000000000:gsVCGG5YbikxYHC7bP5vRvmBqJ7Xz6vG6td"
	// authorizer := client.BotAuthorizer(botToken)

	err := godotenv.Load(".env")
	if err != nil {
		log.Fatalf("Error loading .env file")
	}
	var (
		apiId   = os.Getenv("BUDVA32_API_ID")
		apiHash = os.Getenv("BUDVA32_API_HASH")
	)

	var account accounts.TdInstance
	account.AccountName = "@AndrewKachanov"
	account.TdlibDbDirectory = filepath.Join("tddata", account.AccountName+"-tdlib-db")
	account.TdlibFilesDirectory = filepath.Join("tddata", account.AccountName+"-tdlib-files")

	authorizer.TdlibParameters <- &client.TdlibParameters{
		UseTestDc:              false,
		DatabaseDirectory:      account.TdlibDbDirectory,
		FilesDirectory:         account.TdlibFilesDirectory,
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
		NewVerbosityLevel: 0,
	})

	tdlibClient, err := client.NewClient(authorizer, logVerbosity)
	if err != nil {
		log.Fatalf("NewClient error: %s", err)
	}

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
}

// Receive updates

// tdlibClient, err := client.NewClient(authorizer)
// if err != nil {
//     log.Fatalf("NewClient error: %s", err)
// }

// listener := tdlibClient.GetListener()
// defer listener.Close()

// for update := range listener.Updates {
//     if update.GetClass() == client.ClassUpdate {
//         log.Printf("%#v", update)
//     }
// }

// Proxy support

// proxy := client.WithProxy(&client.AddProxyRequest{
// 	Server: "1.1.1.1",
// 	Port:   1080,
// 	Enable: true,
// 	Type: &client.ProxyTypeSocks5{
// 			Username: "username",
// 			Password: "password",
// 	},
// })

// tdlibClient, err := client.NewClient(authorizer, proxy)

func convertToInt32(s string) int32 {
	i, err := strconv.Atoi(s)
	if err != nil {
		log.Println(err)
		return 0
	}
	return int32(i)
}
