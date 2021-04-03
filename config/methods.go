package config

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"os"

	"github.com/ghodss/yaml"
)

func Load() (*Data, error) {
	var (
		data     *Data
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

	err = json.Unmarshal(jsonData, &data)
	if err != nil {
		log.Printf("Failed to unmarshal file %s: %s", fileName, err)
	}

	if data.PhoneNumber == "" {
		err = errors.New("empty PhoneNumber")
		log.Printf("Failed to read field in file %s: %s", fileName, err)
	}

	return data, err
}
