package app

import (
	"time"

	"github.com/Arman92/go-tdlib"
	"github.com/comerc/budva32/accounts"
)

func HandleMessages() {
	for i := range accounts.TdInstances {
		go func(i int) {
			accounts.TdInstances[i].LoginToTdlib()
			receiver := accounts.TdInstances[i].TdlibClient.AddEventReceiver(&tdlib.UpdateNewMessage{}, accounts.MessageFilter, 10)
			for newMsg := range receiver.Chan {
				accounts.NewMessageHandle(newMsg, accounts.TdInstances[i])
			}
			accounts.CreateUpdateChannel(accounts.TdInstances[i].TdlibClient)
		}(i)
		time.Sleep(300 * time.Millisecond)
	}
}

func Start() {
	// initialise TdInstances var of accounts package
	accounts.ReadAccountsFile()
	// initialise Configs var of config package
	accounts.ReadConfigFile()

	HandleMessages()
}
