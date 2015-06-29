package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/sselph/rp-tool/rw"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
)

var useWeb = flag.Bool("web", false, "Start a web interface.")
var webPort = flag.Int("port", 8080, "Port to use for web interface.")
var homeFolder = flag.String("home", "/home/pi", "The home folder of the user running ES.")
var script = flag.String("script", "", "Script to run on events.")
var version = flag.Bool("version", false, "Print the release version and exit.")

var versionStr string

func main() {
	flag.Parse()

	if *version {
		fmt.Println(versionStr)
		return
	}

	var e rw.Event
	if *useWeb {
		http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			if e.Op != rw.Start {
				return
			}
			b, err := json.MarshalIndent(e, "", "  ")
			if err != nil {
				log.Printf("json:", err)
				return
			}
			fmt.Fprint(w, string(b))
		})

		go http.ListenAndServe(fmt.Sprintf(":%d", *webPort), nil)
	}
	watcher, err := rw.New(*homeFolder)
	if err != nil {
		log.Fatal(err)
	}
	for {
		select {
		case event := <-watcher.Events:
			log.Println("event:", event)
			e = event
			if *script != "" {
				name := e.Game.GameTitle
				if name == "" {
					name = filepath.Base(e.Game.Path)
				}
				c := exec.Command(*script, string(e.Op), e.System.Name, e.System.FullName, name)
				err = c.Run()
				if err != nil {
					log.Printf("cmd.Run: %v", err)
				}
			}
		case err := <-watcher.Errors:
			log.Println("error:", err)
		}
	}
}
