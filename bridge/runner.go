package bridge

import (
	"context"
	"github.com/jessevdk/go-flags"
)

func Run(args []string) error {

	options := &Options{}
	_, err := flags.ParseArgs(options, args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	proxy, err := New(ctx, options)
	if err != nil {
		return err
	}

	srv, err := proxy.Stdio(ctx)
	if err != nil {
		return err
	}
	return srv.ListenAndServe()
}
