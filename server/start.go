package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
)

const (
	Port          = ":9000"
	AdminUser     = "admin"
	AdminPassword = "IForgotMyPassword111"
	Realm         = "Please enter your username and password"
)

func GetIP() string {
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		addrs, _ := i.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			return ip.String()
		}
	}
	return ""
}

func faviconHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/favicon.ico")
}

func configFileHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "config.json")
}

func Start() {
	Host := GetIP()

	http.HandleFunc("/", BasicAuth(helloWorld))
	http.HandleFunc("/new-account/", BasicAuth(addAccountController))
	http.HandleFunc("/accounts/", BasicAuth(accountsController))
	http.HandleFunc("/config/", BasicAuth(configController))

	http.HandleFunc("/config.json", configFileHandler)
	http.HandleFunc("/favicon.ico", faviconHandler)

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static", fs))

	fmt.Println("Web-server is running: http://" + Host + Port)
	err := http.ListenAndServe(Port, http.DefaultServeMux)
	if err != nil {
		log.Fatal("error starting http server : ", err)
		return
	}
}
