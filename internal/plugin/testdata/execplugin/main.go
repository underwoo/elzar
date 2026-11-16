// Small real protocol peer built by the plugin package's TestMain.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	mode := "ok"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "describe-timeout" {
		time.Sleep(10 * time.Second)
	}
	if os.Args[len(os.Args)-1] == "describe" {
		version := 1
		if mode == "protocol" {
			version = 2
		}
		fmt.Printf(`{"protocol":%d,"columns":[{"id":"note","header":"Note","width":12,"tabs":["queue","recent"]}],"sections":[{"id":"extra","title":"Extra"}],"future":true}`, version)
		return
	}
	switch mode {
	case "timeout":
		time.Sleep(10 * time.Second)
		return
	case "exit":
		fmt.Fprintln(os.Stderr, "intentional stderr")
		os.Exit(7)
	case "malformed":
		fmt.Print("{")
		return
	case "oversized":
		fmt.Print(strings.Repeat("x", (1<<20)+1))
		time.Sleep(10 * time.Second)
		return
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("e", (1<<20)+100))
	}
	var request struct {
		Protocol int                        `json:"protocol"`
		Op       string                     `json:"op"`
		Tab      string                     `json:"tab"`
		Jobs     []map[string]string        `json:"jobs"`
		Job      map[string]json.RawMessage `json:"job"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		panic(err)
	}
	if request.Protocol != 1 {
		panic("wrong request version")
	}
	encoder := json.NewEncoder(os.Stdout)
	if request.Op == "columns" {
		values := map[string]map[string]string{}
		for _, job := range request.Jobs {
			values[job["JobID"]] = map[string]string{"note": request.Tab + ":" + job["Name"]}
		}
		if err := encoder.Encode(map[string]any{"values": values}); err != nil {
			panic(err)
		}
	} else if request.Op == "detail" {
		var raw map[string]any
		if err := json.Unmarshal(request.Job["raw"], &raw); err != nil {
			panic(err)
		}
		var name string
		if err := json.Unmarshal(request.Job["Name"], &name); err != nil {
			panic(err)
		}
		if err := encoder.Encode(map[string]any{"sections": map[string][]string{"extra": {name, fmt.Sprint(raw["custom"])}}}); err != nil {
			panic(err)
		}
	} else {
		panic("unknown op")
	}
}
