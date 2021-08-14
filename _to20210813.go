package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"strings"

	"github.com/dgraph-io/badger"
)

func to20210813() {
	uniqueFromTo := make(map[string]string)
	for forwardKey, forward := range configData.Forwards {
		srcChatId := forward.From
		for _, dscChatId := range forward.To {
			key := fmt.Sprintf("%d:%d", srcChatId, dscChatId)
			if _, ok := uniqueFromTo[key]; ok {
				log.Printf("error: double %s", key)
				return
			} else {
				uniqueFromTo[key] = forwardKey
			}
		}
		if forward.Other != 0 {
			dscChatId := forward.Other
			key := fmt.Sprintf("%d:%d", srcChatId, dscChatId)
			if _, ok := uniqueFromTo[key]; ok {
				log.Printf("error: double %s", key)
				return
			} else {
				uniqueFromTo[key] = forwardKey
			}
		}
	}
	{
		file, err := json.MarshalIndent(uniqueFromTo, "", " ")
		if err != nil {
			log.Print(err)
			return
		}
		err = ioutil.WriteFile("1.json", file, 0644)
		if err != nil {
			log.Print(err)
			return
		}
	}
	a := make(map[string][]string)
	err := badgerDB.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()
		prefix := []byte("copiedMsgIds")
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := item.Key()
			val, err := item.ValueCopy(nil)
			if err != nil {
				return err
			}
			k := string(key)
			s := string(val)
			v := strings.Split(s, ",")
			a[k] = v
		}
		return nil
	})
	if err != nil {
		log.Print(err)
		return
	}
	{
		file, err := json.MarshalIndent(a, "", " ")
		if err != nil {
			log.Print(err)
			return
		}
		err = ioutil.WriteFile("2.json", file, 0644)
		if err != nil {
			log.Print(err)
			return
		}
	}
	i1 := 0
	i2 := 0
	result := make(map[string]string)
	for fromChatMessageId, toChatMessageIds := range a {
		// log.Print(fromChatMessageId, toChatMessageIds)
		v := make([]string, 0)
		a := strings.Split(fromChatMessageId, ":")
		srcChatId := a[1]
		for _, toChatMessageId := range toChatMessageIds {
			a := strings.Split(toChatMessageId, ":")
			dstChatId := ""
			dstMessageId := ""
			if len(a) == 2 {
				dstChatId = a[0]
				dstMessageId = a[1]
			} else if len(a) == 3 {
				dstChatId = a[1]
				dstMessageId = a[2]
			} else {
				log.Print("error: len(toChatMessageId)")
				continue
			}
			key := fmt.Sprintf("%s:%s", srcChatId, dstChatId)
			if forwardKey, ok := uniqueFromTo[key]; ok {
				v = append(v, fmt.Sprintf("%s:%s:%s", forwardKey, dstChatId, dstMessageId))
			} else {
				log.Printf("error: forwardKey not found for %s", key)
			}
		}
		key := fromChatMessageId
		vv := make([]string, 0)
		if len(v) > 0 {
			if len(v) == 1 {
				vv = v
			} else {
				for _, toChatMessageId := range v {
					a := strings.Split(toChatMessageId, ":")
					var dstChatId int64
					var tmpMessageId int64
					if len(a) == 2 {
						dstChatId = int64(convertToInt(a[0]))
						tmpMessageId = int64(convertToInt(a[1]))
					} else if len(a) == 3 {
						// forwardKey := a[0]
						// if forwardKey != "FTT-TSLA" {
						// 	continue
						// }
						dstChatId = int64(convertToInt(a[1]))
						tmpMessageId = int64(convertToInt(a[2]))
					} else {
						log.Print("error: len(toChatMessageId)")
						continue
					}
					time.Sleep(1 * time.Second)
					newMessageId := getNewMessageId(dstChatId, tmpMessageId)
					_, err := tdlibClient.GetMessage(&client.GetMessageRequest{
						ChatId:    int64(dstChatId),
						MessageId: newMessageId,
					})
					if err != nil && err.Error() == "404 Not Found" {
						log.Print(err, dstChatId, ":", tmpMessageId)
						deleteNewMessageId(dstChatId, tmpMessageId)
						i1++
					} else {
						i2++
						log.Print("found message ", dstChatId, newMessageId)
						vv = append(vv, toChatMessageId)
					}
				}
			}
		}
		if len(vv) > 0 {
			val := strings.Join(vv, ",")
			setForDB([]byte(key), []byte(val))
			result[key] = val
		} else {
			log.Print("delete ", key)
			deleteForDB([]byte(key))
		}
	}
	{
		file, err := json.MarshalIndent(result, "", " ")
		if err != nil {
			log.Print(err)
			return
		}
		err = ioutil.WriteFile("3.json", file, 0644)
		if err != nil {
			log.Print(err)
			return
		}
	}
	log.Print("****", i1, i2)
}
