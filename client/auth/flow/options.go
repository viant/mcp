package flow

type Options struct {
	scopes        []string
	authURLParams map[string]string
	postParams    map[string]string
	state         string
	codeVerifier  string
}

func (o *Options) Scopes() []string {
	return o.scopes
}

func (o *Options) State() string {
	if o.state != "" {
		return o.state
	}
	o.state = randomToken()
	return o.state
}

func (o *Options) CodeVerifier() (string, error) {
	if o.codeVerifier != "" {
		return o.codeVerifier, nil
	}
	o.codeVerifier = randomToken()
	return o.codeVerifier, nil
}

func NewOptions(opts []Option) *Options {
	ret := &Options{
		scopes:        []string{},
		authURLParams: make(map[string]string),
		postParams:    make(map[string]string),
	}
	for _, opt := range opts {
		opt(ret)
	}
	return ret
}

type Option func(*Options)

func WithScopes(scopes ...string) Option {
	return func(o *Options) {
		o.scopes = append(o.scopes, scopes...)
	}
}
func WithAuthURLParam(key string, value string) Option {
	return func(o *Options) {
		o.authURLParams[key] = value
	}
}

func WithPostParam(key string, value string) Option {
	return func(o *Options) {
		o.postParams[key] = value
	}
}

func WithState(state string) Option {
	return func(o *Options) {
		o.state = state
	}
}
