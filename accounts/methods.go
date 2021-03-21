package accounts

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Arman92/go-tdlib"
	"github.com/joho/godotenv"
)

var TdInstances []TdInstance
var Configs []AccountConfig

func SetUpClient(tdInstance *TdInstance) *tdlib.Client {
	err := godotenv.Load(".env")
	if err != nil {
		log.Println("Error loading .env file")
	}
	return tdlib.NewClient(tdlib.Config{
		APIID:               os.Getenv("BUDVA32_API_ID"),
		APIHash:             os.Getenv("BUDVA32_API_HASH"),
		SystemLanguageCode:  "en",
		DeviceModel:         "Server",
		SystemVersion:       "1.0.0",
		ApplicationVersion:  "1.0.0",
		UseMessageDatabase:  true,
		UseFileDatabase:     true,
		UseChatInfoDatabase: true,
		UseTestDataCenter:   false,
		DatabaseDirectory:   tdInstance.TdlibDbDirectory,
		FileDirectory:       tdInstance.TdlibFilesDirectory,
		IgnoreFileNames:     false,
	})
}

func (tdInstance *TdInstance) LoginToTdlib() {
	if tdInstance.TdlibClient != nil {
		return
	}
	client := SetUpClient(tdInstance)
	tdInstance.TdlibClient = client

	// try till authorize
	for {
		currentState, err := client.Authorize()
		if err != nil {
			fmt.Printf("Error getting current state: %v\n", err)
			continue
		}

		switch currentState.GetAuthorizationStateEnum() {
		case tdlib.AuthorizationStateWaitPhoneNumberType:
			fmt.Print("Enter phone (e.g., 71231234455): ")
			var number string
			fmt.Scanln(&number)
			_, err := client.SendPhoneNumber(number)
			if err != nil {
				fmt.Printf("Error sending phone number: %v\n", err)
			}
		case tdlib.AuthorizationStateWaitCodeType:
			fmt.Print("Enter code (e.g., 01234): ")
			var code string
			fmt.Scanln(&code)
			_, err := client.SendAuthCode(code)
			if err != nil {
				fmt.Printf("Error sending auth code : %v\n", err)
			}
		case tdlib.AuthorizationStateWaitPasswordType:
			fmt.Print("Enter your Password: ")
			var password string
			fmt.Scanln(&password)
			_, err := client.SendAuthPassword(password)
			if err != nil {
				fmt.Printf("Error sending auth password: %v\n", err)
			}
		case tdlib.AuthorizationStateReadyType:
			fmt.Println("Account", tdInstance.AccountName, "successfully authorized!")
			// the only way to out this cycle
			return
		}
	}
}

func InitAccounts() error {
	f, err := os.Open(AccountsFile)
	defer f.Close()
	if err != nil {
		_, err := os.Create(AccountsFile)
		if err != nil {
			return err
		}
	}
	return nil
}

func AddAccountCLI() error {
	var account TdInstance
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Enter a name for this telegram account so you'll remeber it: ")
	text, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	account.AccountName = text[:len(text)-1] // pop back to remove "\n"
	account.TdlibDbDirectory = "./tddata/" + account.AccountName + "-tdlib-db"
	account.TdlibFilesDirectory = "./tddata/" + account.AccountName + "-tdlib-files"

	account.LoginToTdlib()

	ReadAccountsFile()
	TdInstances = append(TdInstances, account)

	jsonTdInstances, err := json.Marshal(TdInstances)
	f, err := os.Create(AccountsFile)
	defer f.Close()
	if err != nil {
		return err
	}
	_, err = io.Copy(f, bytes.NewReader(jsonTdInstances))
	if err != nil {
		return err
	}
	err = AddAccountConfig(account)
	if err != nil {
		return err
	}
	// Handle Ctrl+C
	CtrlCChan := make(chan os.Signal, 2)
	signal.Notify(CtrlCChan, os.Interrupt, syscall.SIGTERM)
	go func(ac TdInstance) {
		<-CtrlCChan
		ac.TdlibClient.DestroyInstance()
		os.Exit(0)
	}(account)
	return nil
}

func AddAccountConfig(acc TdInstance) error {
	var newConfig AccountConfig
	c, err := acc.TdlibClient.GetMe()
	if err != nil {
		return err
	}
	newConfig.Account = c.PhoneNumber
	newConfig.Forwards = []Forward{
		{From: 123123, To: []int64{123, 123}},
		{From: 33221100, To: []int64{123}},
	}
	ReadConfigFile()
	Configs = append(Configs, newConfig)

	jsonConfigs, err := json.Marshal(Configs)
	f, err := os.Create(ConfigFile)
	defer f.Close()
	if err != nil {
		return err
	}
	_, err = io.Copy(f, bytes.NewReader(jsonConfigs))
	if err != nil {
		return err
	}
	return nil
}

func ReadAccountsFile() {
	f, err := os.Open(AccountsFile)
	defer f.Close()
	if err != nil {
		log.Println("Failed to open file "+AccountsFile+":", err)
	}

	err = json.NewDecoder(f).Decode(&TdInstances)
	if err != nil && err != io.EOF { // TODO: refactor all this shit
		log.Println("Failed to unmarshal "+AccountsFile+":", err)
	}
}

func DeleteAccount(name string) error {
	ReadAccountsFile()
	var ActiveAccounts []TdInstance
	for i := range TdInstances {
		if TdInstances[i].AccountName != name {
			ActiveAccounts = append(ActiveAccounts, TdInstances[i])
		}
	}
	TdInstances = ActiveAccounts
	jsonTdInstances, err := json.Marshal(TdInstances)
	f, err := os.Create(AccountsFile)
	defer f.Close()
	if err != nil {
		return err
	}
	_, err = io.Copy(f, bytes.NewReader(jsonTdInstances))
	if err != nil {
		return err
	}
	return nil
}

func GetAccounts() []string {
	var names []string
	if TdInstances == nil {
		ReadAccountsFile()
	}
	for _, v := range TdInstances {
		names = append(names, v.AccountName)
	}
	return names
}

func CreateUpdateChannel(client *tdlib.Client) {
	rawUpdates := client.GetRawUpdatesChannel(100)
	for update := range rawUpdates {
		// TODO: remove fmt.Println
		fmt.Println("update", update)
		_ = update
	}
}

func ReadConfigFile() {
	f, err := os.Open(ConfigFile)
	defer f.Close()
	if err != nil {
		log.Println("Failed to open file "+ConfigFile+":", err)
	}

	err = json.NewDecoder(f).Decode(&Configs)
	if err != nil && err != io.EOF { // TODO: refactor all this shit
		log.Println("Failed to unmarshal "+ConfigFile+":", err)
	}
}

func InitConfig() error {
	f, err := os.Open(ConfigFile)
	defer f.Close()
	if err != nil {
		_, err := os.Create(ConfigFile)
		if err != nil {
			return err
		}
	}
	return nil
}
