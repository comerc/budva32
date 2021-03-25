package account

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"os"

	"github.com/ghodss/yaml"
)

var Config AccountConfig

func ReadConfigFile() error {
	var (
		err      error
		file     *os.File
		yamlData []byte
		jsonData []byte
	)

	file, err = os.Open(ConfigFile)
	if err != nil {
		log.Printf("Failed to open file %s: %s", ConfigFile, err)
	}
	defer file.Close()

	yamlData, err = ioutil.ReadAll(file)
	if err != nil {
		log.Printf("Failed to read file %s: %s", ConfigFile, err)
	}

	jsonData, err = yaml.YAMLToJSON(yamlData)
	if err != nil {
		log.Printf("Failed to convert file %s with YAMLToJSON: %s", ConfigFile, err)
	}

	err = json.Unmarshal(jsonData, &Config)
	if err != nil {
		log.Printf("Failed to unmarshal file %s: %s", ConfigFile, err)
	}

	if Config.PhoneNumber == "" {
		err = errors.New("empty PhoneNumber")
		log.Printf("Failed to read field in file %s: %s", ConfigFile, err)
	}

	return err
}
