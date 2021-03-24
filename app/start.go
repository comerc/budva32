package app

import (
	"fmt"
	"time"

	"github.com/Arman92/go-tdlib"
	"github.com/comerc/budva32/accounts"
)

func HandleMessages() {
	for i := range accounts.TdInstances {
		fmt.Println("HandleMessages", i)
		// fmt.Println("LoginToTdlib")
		// accounts.TdInstances[i].LoginToTdlib()
		go func(i int) {
			receiver := accounts.TdInstances[i].TdlibClient.AddEventReceiver(&tdlib.UpdateNewMessage{}, accounts.NewMessageFilter, 10)
			for newMessage := range receiver.Chan {
				accounts.NewMessageHandle(newMessage, accounts.TdInstances[i])
			}
		}(i)
		go func(i int) {
			receiver := accounts.TdInstances[i].TdlibClient.AddEventReceiver(&tdlib.UpdateMessageEdited{}, accounts.MessageEditedFilter, 10)
			for messageEdited := range receiver.Chan {
				accounts.MessageEditedHandle(messageEdited, accounts.TdInstances[i])
			}
		}(i)
		// go func(i int) {
		// 	accounts.CreateUpdateChannel(accounts.TdInstances[i].TdlibClient)
		// }(i)
		fmt.Println("TdInstances")
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
