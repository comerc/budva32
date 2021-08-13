package main

import (
	"encoding/json"
	"fmt"
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
	}
	// log.Printf("%#v", uniqueFromTo)
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
	for fromChatMessageId, toChatMessageIds := range a {
		v := make([]string, 0)
		a := strings.Split(fromChatMessageId, ":")
		srcChatId := a[1]
		for _, toChatMessageId := range toChatMessageIds {
			a := strings.Split(toChatMessageId, ":")
			dstChatId := a[0]
			key := fmt.Sprintf("%s:%s", srcChatId, dstChatId)
			if forwardKey, ok := uniqueFromTo[key]; ok {
				v = append(v, fmt.Sprintf("%s:%s", forwardKey, toChatMessageId))
			} else {
				log.Printf("error: forwardKey not found for %s", key)
			}
		}
		key := []byte(fromChatMessageId)
		if len(v) > 0 {
			val := []byte(strings.Join(v, ","))
			setForDB(key, val)
		} else {
			deleteForDB(key)
		}
	}
	v, err := json.Marshal(a)
	if err != nil {
		log.Print(err)
	}
	log.Print(string(v))
}
