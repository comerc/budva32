package accounts

import "github.com/Arman92/go-tdlib"

const AccountsFile = "accounts.json"

type TdInstance struct {
	AccountName         string        `json:"AccountName"`
	TdlibDbDirectory    string        `json:"TdlibDbDirectory"`
	TdlibFilesDirectory string        `json:"TdlibFilesDirectory"`
	TdlibClient         *tdlib.Client `json:"-"`
}

const ConfigFile = "config.json"

type AccountConfig struct {
	Account  string    `json:"account"`
	Forwards []Forward `json:"forwards"`
}

type Forward struct {
	From int64   `json:"from"`
	To   []int64 `json:"to"`
}
