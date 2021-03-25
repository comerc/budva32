package accounts

import "github.com/zelenin/go-tdlib/client"

const AccountsFile = "accounts.json"

type TDInstance struct {
	PhoneNumber            string         `json:"PhoneNumber"`
	TDLibDatabaseDirectory string         `json:"TDLibDatabaseDirectory"`
	TDLibFilesDirectory    string         `json:"TDLibFilesDirectory"`
	TDLibClient            *client.Client `json:"-"`
}

const ConfigFile = "config.json"

type Config struct {
	PhoneNumber string    `json:"PhoneNumber"`
	Forwards    []Forward `json:"Forwards"`
}

type Forward struct {
	From int64   `json:"From"`
	To   []int64 `json:"To"`
}
