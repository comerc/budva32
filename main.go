package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Arman92/go-tdlib"
	"github.com/comerc/budva32/accounts"
	"github.com/comerc/budva32/menu"
)

func main() {
	var err error
	tdlib.SetLogVerbosityLevel(1)
	tdlib.SetFilePath("./errors.txt")

	err = accounts.InitConfig()
	accounts.ReadConfigFile()
	if err != nil {
		fmt.Println("Can't initialise config:", err)
	}

	err = accounts.InitAccounts()
	accounts.ReadAccountsFile()
	if err != nil {
		fmt.Println("Can't initialise accounts:", err)
	}

	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// Handle Ctrl+C
	if len(accounts.TdInstances) > 0 {
		for i := range accounts.TdInstances {
			accounts.TdInstances[i].LoginToTdlib()
			go func(i int) {
				<-c
				tdlib.IsClosed = true
				time.Sleep(1 * time.Second)
				accounts.TdInstances[i].TdlibClient.DestroyInstance()
				os.Exit(0)
			}(i)
		}
	} else {
		go func() {
			<-c
			os.Exit(0)
		}()
	}

	for {
		menu.CallMenu()
	}
}
