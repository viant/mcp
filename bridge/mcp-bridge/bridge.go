package main

import (
	"github.com/viant/mcp/bridge"
	_ "github.com/viant/scy/kms/blowfish"
	"log"
	"os"
)

func main() {
	if err := bridge.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

}
