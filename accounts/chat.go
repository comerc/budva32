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

func NewMessageFilter(msg *tdlib.TdMessage) bool {
	updateMsg := (*msg).(*tdlib.UpdateNewMessage)
	// fmt.Println(updateMsg.Type)
	if updateMsg.Message.IsOutgoing == false {
		return true
	}
	return false
}

var messageIDs = make(map[string]int64)

func setMessageID(srcChatID, srcMessageID, dscChatID, dscMessageID int64) {
	// messageIDs[fmt.Sprintf("%d:%d:%d", srcChatID, srcMessageID, dscChatID)] = fmt.Sprint(dscMessageID)
	messageIDs[fmt.Sprintf("%d:%d:%d", srcChatID, srcMessageID, dscChatID)] = dscMessageID
}
func getMessageID(srcChatID, srcMessageID, dscChatID int64) int64 {
	// i, _ := strconv.Atoi(messageIDs[fmt.Sprintf("%d:%d:%d", srcChatID, srcMessageID, dscChatID)])
	// return int64(i)
	return messageIDs[fmt.Sprintf("%d:%d:%d", srcChatID, srcMessageID, dscChatID)]
}

func NewMessageHandle(newMessage interface{}, acc TdInstance) {
	state := tdlib.NewMessageSchedulingStateSendAtDate(int32(time.Now().Unix()))
	options := tdlib.NewMessageSendOptions(false, false, state)
	updateMsg := (newMessage).(*tdlib.UpdateNewMessage)
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
						forwardedMessages, err := acc.TdlibClient.ForwardMessages(to,
							forward.From,
							[]int64{updateMsg.Message.ID},
							options,
							true,
							false)
						if err != nil {
							fmt.Println(err)
						}
						if forwardedMessages.TotalCount != 1 {
							fmt.Println("Invalid TotalCount")
						} else {
							forwardedMessage := forwardedMessages.Messages[0]
							fmt.Println("setMessageID: ", updateMsg.Message.ChatID, updateMsg.Message.ID,
								to, forwardedMessage.ID)
							setMessageID(updateMsg.Message.ChatID, updateMsg.Message.ID,
								to, forwardedMessage.ID)

							// formattedText := updateMsg.Message.Content.(*tdlib.MessageText).Text
							// inputMessageContent := tdlib.NewInputMessageText(formattedText, false, false)
							// forwardedMessage, err := acc.TdlibClient.SendMessage(to, 0, 0, options, nil, inputMessageContent)
							// if err != nil {
							// 	fmt.Println(err)
							// } else {
							// 	fmt.Println("SendMessage: ", forwardedMessage.ID)
							// 	setMessageID(updateMsg.Message.ChatID, updateMsg.Message.ID,
							// 		to, forwardedMessage.ID)
							// }

							// messageID := forwardedMessage.ID
							// go func(to, messageID int64) {
							// 	time.Sleep(1 * time.Second)
							// 	messageLink, err1 := acc.TdlibClient.GetMessageLink(to, messageID, false, false)
							// 	if err1 != nil {
							// 		fmt.Println(err1)
							// 	} else {
							// 		fmt.Println("GetMessageLink: ", messageLink.Link)
							// 	}
							// }(to, messageID)

							// messageID := forwardedMessage.ID
							// dscMsg, err1 := acc.TdlibClient.GetMessage(to, messageID)
							// if err1 != nil {
							// 	fmt.Println(err1)
							// }
							// fmt.Println("dscMsg: ", dscMsg)

							// editedMsg, err := acc.TdlibClient.GetMessage(updateMsg.Message.ChatID, updateMsg.Message.ID)
							// if err != nil {
							// 	fmt.Println(err)
							// }
							// fmt.Println("editedMsg: ", editedMsg)
							// formattedText := editedMsg.Content.(*tdlib.MessageText).Text
							// formattedText.Text = "1111"

							// messageID := getMessageID(updateMsg.Message.ChatID, updateMsg.Message.ID, to)
							// if messageID == 0 {
							// 	continue
							// }

							// inputMessageContent := tdlib.NewInputMessageText(formattedText, false, false)
							// fmt.Println("EditMessageText: ", to, messageID)
							// _, err = acc.TdlibClient.EditMessageText(to, messageID,
							// 	nil,
							// 	inputMessageContent)
							// if err != nil {
							// 	fmt.Println(err)
							// }
						}
					}
				}
			}
			break
		}
	}
}

func MessageEditedFilter(msg *tdlib.TdMessage) bool {
	// updateMsg := (*msg).(*tdlib.UpdateMessageEdited)
	// fmt.Println("MessageEditedFilter: ", updateMsg.MessageID)
	return true
}

func MessageEditedHandle(messageEdited interface{}, acc TdInstance) {
	// state := tdlib.NewMessageSchedulingStateSendAtDate(int32(time.Now().Unix()))
	// options := tdlib.NewMessageSendOptions(false, false, state)
	updateMsg := (messageEdited).(*tdlib.UpdateMessageEdited)
	c, err := acc.TdlibClient.GetMe()
	if err != nil {
		fmt.Println(err)
	}
	for _, con := range Configs {
		if con.Account == string(c.PhoneNumber) {
			forwards := con.Forwards
			for _, forward := range forwards {
				if updateMsg.ChatID == forward.From {
					fmt.Println(c.PhoneNumber, "- Message ", updateMsg.MessageID, " edited from ", updateMsg.ChatID)
					// editedMsg, err := acc.TdlibClient.GetMessage(updateMsg.ChatID, updateMsg.MessageID)
					// if err != nil {
					// 	fmt.Println(err)
					// }
					// formattedText := editedMsg.Content.(*tdlib.MessageText).Text
					// fmt.Println(editedMsg.Content.(*tdlib.MessageText).Text.Text)
					for _, to := range forward.To {
						messageID := getMessageID(updateMsg.ChatID, updateMsg.MessageID, to)
						if messageID == 0 {
							continue
						}
						// fmt.Println(dscMsg.Content.(*tdlib.MessageText).Text.Text)
						// time.Sleep(20 * time.Second)
						// fmt.Println("dscMsg: ", to, messageID)

						// dscMsg, err1 := acc.TdlibClient.GetMessage(to, messageID)
						// if err1 != nil {
						// 	fmt.Println(err1, dscMsg)
						// }

						// inputMessageContent := tdlib.NewInputMessageText(formattedText, false, false)
						// fmt.Println("EditMessageText: ", to, messageID)
						// _, err := acc.TdlibClient.EditMessageText(to, messageID,
						// 	nil,
						// 	inputMessageContent)
						// if err != nil {
						// 	fmt.Println(err)
						// }
					}
				}
			}
			break
		}
	}
}
