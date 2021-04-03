package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ghodss/yaml"
	"github.com/radovskyb/watcher"
)

var path = filepath.Join(".", fileName)

func Load() (*[]Forward, error) {
	var (
		forwards *[]Forward
		err      error
		file     *os.File
		yamlData []byte
		jsonData []byte
	)

	file, err = os.Open(path)
	if err != nil {
		log.Printf("Failed to open file %s: %s", path, err)
	}
	defer file.Close()

	yamlData, err = ioutil.ReadAll(file)
	if err != nil {
		log.Printf("Failed to read file %s: %s", path, err)
	}

	jsonData, err = yaml.YAMLToJSON(yamlData)
	if err != nil {
		log.Printf("Failed to convert file %s with YAMLToJSON: %s", path, err)
	}

	err = json.Unmarshal(jsonData, &forwards)
	if err != nil {
		log.Printf("Failed to unmarshal file %s: %s", path, err)
	}

	for _, forward := range *forwards {
		for _, dscChatId := range forward.To {
			if forward.From == dscChatId {
				err := fmt.Errorf("destination Id cannot be equal to source Id %d", dscChatId)
				return nil, err
			}
		}
	}

	return forwards, err
}

type Reload func()

func Watch(reload Reload) {
	w := watcher.New()

	// SetMaxEvents to 1 to allow at most 1 event's to be received
	// on the Event channel per watching cycle.
	//
	// If SetMaxEvents is not set, the default is to send all events.
	w.SetMaxEvents(1)

	// Only notify write events.
	w.FilterOps(watcher.Write)

	go func() {
		for {
			select {
			case event := <-w.Event:
				// fmt.Println(event) // Print the event's info.
				_ = event
				reload()
			case err := <-w.Error:
				log.Fatalln(err)
			case <-w.Closed:
				return
			}
		}
	}()

	// Watch this path for changes.
	if err := w.Add(path); err != nil {
		log.Fatalln(err)
	}

	reload()

	// Start the watching process - it'll check for changes every 1000ms.
	if err := w.Start(1000 * time.Millisecond); err != nil {
		log.Fatalln(err)
	}
}
