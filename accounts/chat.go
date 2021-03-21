package accounts

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/Arman92/go-tdlib"
)

const messageLength int = 80

func GetAllChatLists(limit int) (map[string][]map[string]string, error) {
	allAccountsChats := make(map[string][]map[string]string)
	for _, acc := range TdInstances {
		chats, err := GetAccountChatList(acc, limit)
		if err != nil {
			return allAccountsChats, err
		}
		allAccountsChats[acc.AccountName] = chats
	}
	return allAccountsChats, nil
}

func GetAccountChatList(acc TdInstance, limit int) ([]map[string]string, error) {
	offsetOrder := int64(math.MaxInt64)
	offsetChatID := int64(0)
	var chat map[string]string
	var chatsStringArr []map[string]string
	acc.LoginToTdlib()
	var chatList = tdlib.NewChatListMain()
	chats, err := acc.TdlibClient.GetChats(chatList, tdlib.JSONInt64(offsetOrder),
		offsetChatID, int32(limit))
	if err != nil {
		return chatsStringArr, err
	}
	for _, id := range chats.ChatIDs {
		c, err := acc.TdlibClient.GetChat(id)
		if err != nil {
			return chatsStringArr, err
		}
		lastmsg := ""
		if msg, ok := c.LastMessage.Content.(*tdlib.MessageText); ok {
			if len(msg.Text.Text) >= messageLength {
				lastmsg = msg.Text.Text[:messageLength] + "..."
			} else {
				lastmsg = msg.Text.Text
			}
		}
		chat = map[string]string{
			"id":      strconv.FormatInt(id, 10),
			"title":   c.Title,
			"lastmsg": lastmsg,
		}
		chatsStringArr = append(chatsStringArr, chat)
	}
	return chatsStringArr, nil
}

func MessageFilter(msg *tdlib.TdMessage) bool {
	updateMsg := (*msg).(*tdlib.UpdateNewMessage)
	if updateMsg.Message.IsOutgoing == false {
		return true
	}
	return false
}

func NewMessageHandle(newMsg interface{}, acc TdInstance) {
	state := tdlib.NewMessageSchedulingStateSendAtDate(int32(time.Now().Unix()))
	options := tdlib.NewMessageSendOptions(false, false, state)
	updateMsg := (newMsg).(*tdlib.UpdateNewMessage)
	c, err := acc.TdlibClient.GetMe()
	if err != nil {
		fmt.Println(err)
	}
	for _, con := range Configs {
		if con.Account == string(c.PhoneNumber) {
			forwards := con.Forwards
			for _, forward := range forwards {
				if updateMsg.Message.ChatID == forward.From {
					fmt.Println(c.PhoneNumber, "- Message ", updateMsg.Message.ID, " forwarded from ", updateMsg.Message.ChatID)
					for _, to := range forward.To {
						_, err := acc.TdlibClient.ForwardMessages(to,
							forward.From,
							[]int64{updateMsg.Message.ID},
							options,
							true,
							false)
						if err != nil {
							fmt.Println(err)
						}
					}
				}
			}
			break
		}
	}
}
