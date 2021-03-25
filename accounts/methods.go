package accounts

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"os"

	"github.com/ghodss/yaml"
)

var Configs []Config

func ReadConfigFile() error {
	var (
		err      error
		file     *os.File
		yamlData []byte
		jsonData []byte
	)

	file, err = os.Open(ConfigFile)
	if err != nil {
		log.Println("Failed to open file "+ConfigFile+":", err)
	}
	defer file.Close()

	yamlData, err = ioutil.ReadAll(file)
	if err != nil {
		log.Println("Failed to read file "+ConfigFile+":", err)
	}

	jsonData, err = yaml.YAMLToJSON(yamlData)
	if err != nil {
		log.Println("Failed to convert with YAMLToJSON:", err)
	}

	err = json.Unmarshal(jsonData, &Configs)
	if err != nil {
		log.Println("Failed to unmarshal file "+ConfigFile+":", err)
	}

	if len(Configs) == 0 {
		err = errors.New("empty Configs")
		log.Println("Failed to unmarshal file "+ConfigFile+":", err)
	}

	if Configs[0].PhoneNumber == "" {
		err = errors.New("empty PhoneNumber")
		log.Println("Failed to unmarshal file "+ConfigFile+":", err)
	}

	return err
}
