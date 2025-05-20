package main

import (
	"github.com/viant/mcp/bridge"
	_ "github.com/viant/scy/kms/blowfish"
	"log"
	"os"
)

func main() {

	//os.Args = []string{"", "-u", "http://localhost:5000/sse", "-c", "idp_viant.enc|blowfish://default"}

	if err := bridge.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

}

/*
{"id":1,"jsonrpc":"2.0","method":"initialize","params":{"capabilities":{},"clientInfo":{"name":"tester","version":"0.1"},"protocolVersion":"2025-03-26"}}
{"id":1,"jsonrpc":"2.0","result":{"capabilities":{"resources":{},"tools":{}},"protocolVersion":"2025-03-26","serverInfo":{"name":"Datly","version":"0.1"}}}

{"id":3, "method": "resources/read", "params": {"uri":"datly://localhost/v1/api/guardian/performance/supply/prod"}}
{"code":-32700,"data":{"id":3,"method":"resources/read","params":{"uri":"datly://localhost/v1/api/guardian/performance/supply/prod"}},"message":"failed to parse: field jsonrpc in Request: required"}
{"id":3, "jsonrpc":"2.0", "method": "resources/read", "params": {"uri":"datly://localhost/v1/api/guardian/performance/supply/prod"}}
{"id":3,"jsonrpc":"2.0","error":{"code":-32603,"message":"code: -32603, message: failed to send request: Post \"http://localhost:5000/message?session_id=cf0f9d96-9438-4948-aa64-476c26566c78\": failed to decode metadata: invalid character 'e' looking for beginning of value, data: []"},"result":null}
{"id":3, "jsonrpc":"2.0", "method": "resources/read", "params": {"uri":"datly://localhost/v1/api/guardian/performance/supply/prod"}}
{"id":3,"jsonrpc":"2.0","error":{"code":-32603,"message":"code: -32603, message: failed to send request: Post \"http://localhost:5000/message?session_id=cf0f9d96-9438-4948-aa64-476c26566c78\": failed to decode metadata: invalid character 'e' looking for beginning of value, data: []"},"result":null}
^C
(base) awitas@AWITAS-VLXV6VKM19 ui %




*/
