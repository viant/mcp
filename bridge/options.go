package bridge

type Options struct {
	URL             string `short:"u" long:"url" description:"mcp url" required:"true"`
	OAuth2ConfigURL string `short:"c" long:"config" description:"oauth2 config file"`
}
