package account

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"os"

	"github.com/ghodss/yaml"
)

var config Config

func Load() error {
	var (
		err      error
		file     *os.File
		yamlData []byte
		jsonData []byte
	)

	file, err = os.Open(fileName)
	if err != nil {
		log.Printf("Failed to open file %s: %s", fileName, err)
	}
	defer file.Close()

	yamlData, err = ioutil.ReadAll(file)
	if err != nil {
		log.Printf("Failed to read file %s: %s", fileName, err)
	}

	jsonData, err = yaml.YAMLToJSON(yamlData)
	if err != nil {
		log.Printf("Failed to convert file %s with YAMLToJSON: %s", fileName, err)
	}

	err = json.Unmarshal(jsonData, &config)
	if err != nil {
		log.Printf("Failed to unmarshal file %s: %s", fileName, err)
	}

	if len(config.Accounts) == 0 || config.Accounts[0].PhoneNumber == "" {
		err = errors.New("empty Accounts")
		log.Printf("Failed to read field in file %s: %s", fileName, err)
	}

	return err
}

func GetAccounts() []Account {
	return config.Accounts
}
