package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode"

	"github.com/Arman92/go-tdlib"
	"github.com/comerc/budva32/accounts"
)

func helloWorld(w http.ResponseWriter, r *http.Request) {
	t := template.Must(template.ParseFiles("./templates/pages/index.html",
		"./templates/head.html",
		"./templates/menu.html",
		"./templates/footer.html"))
	data := map[string]interface{}{
		"Title": "Welcome!",
	}
	t.Execute(w, data)
}

func viewAllAccounts(w http.ResponseWriter, r *http.Request) {
	acs := accounts.GetAccounts()
	t := template.Must(template.ParseFiles("./templates/pages/accounts.html",
		"./templates/head.html",
		"./templates/menu.html",
		"./templates/footer.html"))
	data := map[string]interface{}{
		"Title":    "Accounts",
		"Accounts": acs,
	}
	t.Execute(w, data)
}

func viewChatList(w http.ResponseWriter, r *http.Request, name string) {
	var chats []map[string]string
	for i := range accounts.TdInstances {
		if accounts.TdInstances[i].AccountName == name {
			accounts.TdInstances[i].LoginToTdlib()
			chatList, err := accounts.GetAccountChatList(accounts.TdInstances[i], 100)
			if err != nil {
				fmt.Fprintf(w, err.Error())
			}
			chats = chatList // pls dont ask
			break
		}
	}
	t := template.Must(template.ParseFiles("./templates/pages/chats.html",
		"./templates/head.html",
		"./templates/menu.html",
		"./templates/footer.html"))
	data := map[string]interface{}{
		"Title": name + "'s chats",
		"Chats": chats,
	}
	t.Execute(w, data)
}

func addAccountController(w http.ResponseWriter, r *http.Request) {
	p := strings.Split(r.URL.Path, "/")
	p = p[1:] // first element is empty

	if len(p) == 1 {
		addAccountForm(w, r)
	} else if len(p) > 1 {
		if p[1] != "" {
			addAccount(w, r, p[1])
		} else {
			addAccountForm(w, r)
		}
	} else {
		fmt.Fprintf(w, "No account in URL path")
	}
}

func addAccountForm(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		r.ParseForm()
		name := r.PostForm["name"][0]
		http.Redirect(w, r, "/new-account/"+name, 303)
	}
	t := template.Must(template.ParseFiles("./templates/pages/new_account.html",
		"./templates/head.html",
		"./templates/menu.html",
		"./templates/footer.html"))
	data := map[string]string{
		"Title": "Add account",
	}
	t.Execute(w, data)
}

func addAccount(w http.ResponseWriter, r *http.Request, name string) {
	// TODO: refactor this spaghetti-code
	if len(accounts.TdInstances) > 0 {
		for i := range accounts.TdInstances {
			if accounts.TdInstances[i].AccountName == name {
				fmt.Fprintf(w, "Account already authorised")
				return
			}
		}
	}

	var account accounts.TdInstance
	var state tdlib.AuthorizationStateEnum
	var data string

	account.AccountName = name
	account.TdlibDbDirectory = "./tddata/" + account.AccountName + "-tdlib-db"
	account.TdlibFilesDirectory = "./tddata/" + account.AccountName + "-tdlib-files"

	if account.TdlibClient == nil {
		client := accounts.SetUpClient(&account)
		account.TdlibClient = client
	}
	client := account.TdlibClient

	t := template.Must(template.ParseFiles("./templates/pages/new_account.html",
		"./templates/head.html",
		"./templates/menu.html",
		"./templates/footer.html"))

	for {
		currentState, err := client.Authorize()
		if err != nil {
			fmt.Fprintf(w, "Error getting current state: %v\n", err)
		}
		state = currentState.GetAuthorizationStateEnum()
		switch state {
		case tdlib.AuthorizationStateWaitPhoneNumberType:
			data = "Enter phone (e.g., 71231234455): "
			if r.Method == "POST" {
				r.ParseForm()
				phone := r.PostForm["phone"][0]
				_, err := client.SendPhoneNumber(phone)
				if err != nil {
					data = "Error sending phone phone: %v\n" + err.Error()
				}
				time.Sleep(time.Second * 1)
			}
			context := map[string]interface{}{
				"Title":    "Add account " + name,
				"Phone":    true,
				"Code":     false,
				"Password": false,
				"Data":     data,
				"Name":     name,
			}
			t.Execute(w, context)
			return
		case tdlib.AuthorizationStateWaitCodeType:
			data = "Enter code (e.g., 01234): "
			if r.Method == "POST" {
				r.ParseForm()
				code := r.PostForm["code"][0]
				_, err := client.SendAuthCode(code)
				if err != nil {
					fmt.Printf("Error sending auth code : %v\n", err)
				}
				time.Sleep(time.Second * 1)
			}
			context := map[string]interface{}{
				"Title":    "Add account " + name,
				"Phone":    false,
				"Code":     true,
				"Password": false,
				"Data":     data,
				"Name":     name,
			}
			t.Execute(w, context)
			return
		case tdlib.AuthorizationStateWaitPasswordType:
			data = "Enter your Password: "
			if r.Method == "POST" {
				r.ParseForm()
				password := r.PostForm["password"][0]
				_, err := client.SendAuthPassword(password)
				if err != nil {
					fmt.Printf("Error sending auth password: %v\n", err)
				}
				time.Sleep(time.Second * 1)
			}
			context := map[string]interface{}{
				"Title":    "Add account " + name,
				"Phone":    false,
				"Code":     false,
				"Password": true,
				"Data":     data,
				"Name":     name,
			}
			t.Execute(w, context)
			return
		case tdlib.AuthorizationStateReadyType:
			// if account is new and authorised
			accounts.ReadConfigFile()
			accounts.TdInstances = append(accounts.TdInstances, account)

			jsonTdInstances, err := json.Marshal(accounts.TdInstances)
			f, err := os.Create(accounts.AccountsFile)
			defer f.Close()
			if err != nil {
				fmt.Fprintf(w, err.Error())
			}
			_, err = io.Copy(f, bytes.NewReader(jsonTdInstances))
			if err != nil {
				fmt.Fprintf(w, err.Error())
			}
			err = accounts.AddAccountConfig(account)
			if err != nil {
				fmt.Fprintf(w, err.Error())
			}
			// Handle Ctrl+C
			CtrlCChan := make(chan os.Signal, 2)
			signal.Notify(CtrlCChan, os.Interrupt)
			go func(ac *accounts.TdInstance) {
				<-CtrlCChan
				ac.TdlibClient.DestroyInstance()
				os.Exit(0)
			}(&account)
			http.Redirect(w, r, "/accounts/", 303)
			return
		}
	}
}

func accountsController(w http.ResponseWriter, r *http.Request) {
	p := strings.Split(r.URL.Path, "/")
	p = p[1:] // first element is empty

	if len(p) == 1 {
		viewAllAccounts(w, r)
	} else if len(p) > 1 {
		if p[1] != "" {
			viewChatList(w, r, p[1])
		} else {
			viewAllAccounts(w, r)
		}
	} else {
		fmt.Fprintf(w, "No account in URL path")
	}
}

func configController(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var err error

		r.ParseForm()
		config := r.PostForm["config"][0]

		config = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, config)
		err = json.Unmarshal([]byte(config), &accounts.Configs)
		if err != nil {
			fmt.Fprintf(w, "Error while unmarshaling JSON file: %v\n File: %v", err.Error(), config)
			return
		}
		f, _ := os.Create(accounts.ConfigFile)
		defer f.Close()
		io.Copy(f, bytes.NewReader([]byte(config)))
		//accounts.ReadConfigFile()

		viewConfigFile(w, r)
	} else {
		viewConfigFile(w, r)
	}
}

func viewConfigFile(w http.ResponseWriter, r *http.Request) {
	accounts.ReadConfigFile()
	config, _ := json.MarshalIndent(accounts.Configs, "", "\t")

	t := template.Must(template.ParseFiles("./templates/pages/config.html",
		"./templates/head.html",
		"./templates/menu.html",
		"./templates/footer.html"))
	data := map[string]string{
		"Title":  "Config",
		"Config": string(config),
	}
	t.Execute(w, data)
}
