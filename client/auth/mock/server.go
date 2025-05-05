package mock

import "net/http/httptest"

type HTTPTestAuthorizationServer struct {
	*AuthorizationService
	Server *httptest.Server
	Issuer string
}

func NewHTTPTestAuthorizationServer() (*HTTPTestAuthorizationServer, error) {
	service, err := NewAuthorizationService()
	if err != nil {
		return nil, err
	}
	server := &HTTPTestAuthorizationServer{
		AuthorizationService: service,
	}
	server.Server = httptest.NewServer(service.Handler())
	service.Issuer = server.Server.URL
	server.Issuer = server.Server.URL
	return server, nil
}

func (s *HTTPTestAuthorizationServer) Close() {
	if s.Server != nil {
		s.Server.Close()
	}
	s.AuthorizationService = nil
	s.Server = nil
}
