package menu

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Arman92/go-tdlib"
	"github.com/comerc/budva32/accounts"
	"github.com/comerc/budva32/app"
	"github.com/comerc/budva32/server"
)

func CallMenu() {
	menu := "Menu:\n" +
		"\tla                   [L]ist telegram [a]ccounts\n" +
		"\tda <account>         [D]elete [a]ccount by name\n" +
		"\taa                   [A]dd new [a]ccount\n" +
		"\tsc                   [S]how [c]hats\n" +
		"\n" +
		"\tw                    Start [W]eb-server\n" +
		"\ts                    [S]tart the app\n" +
		"\tt                    S[t]op the app\n" +
		"\tr                    [R]ead new settings\n" +
		"\te                    [E]xit\n"

	fmt.Println(strings.Repeat("#", 42))
	fmt.Println(menu)
	fmt.Println(strings.Repeat("#", 42))

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = input[:len(input)-1] // pop back to remove "\n"
	inputs := strings.Split(input, " ")

	switch inputs[0] {
	case "la":
		names := accounts.GetAccounts()
		fmt.Println("Available accounts:")
		if names == nil {
			fmt.Println("None accounts available")
		}
		for i, n := range names {
			i++
			fmt.Println(i, n)
		}
	case "da":
		accounts.DeleteAccount(inputs[1])
	case "aa":
		accounts.AddAccountCLI()
	case "sc":
		//chats := accounts.UpdateAllChatLists()
		chats, err := accounts.GetAllChatLists(100)
		if err != nil {
			fmt.Println(err) // TODO: add error handling. Maybe.
		}
		for acc, chatsArray := range chats {
			fmt.Println(acc + "'s chats:")
			for i, chat := range chatsArray {
				i++
				fmt.Println(i, chat["id"], chat["title"], chat["lastmsg"])
			}
		}
	case "w":
		go server.Start()
	case "s":
		app.Start()
	case "t":
		tdlib.IsClosed = true
		time.Sleep(1 * time.Second)
		for i := range accounts.TdInstances {
			accounts.TdInstances[i].TdlibClient.DestroyInstance()
		}
	case "r":
		fmt.Println("Reading new config")
		accounts.ReadConfigFile()
	case "e":
		tdlib.IsClosed = true
		time.Sleep(1 * time.Second)
		for i := range accounts.TdInstances {
			accounts.TdInstances[i].TdlibClient.DestroyInstance()
		}
		fmt.Println("Goodbye!")
		os.Exit(0)
	default:
		fmt.Println("Unknown command. Please, try again:")
	}
}
