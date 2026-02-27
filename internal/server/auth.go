package server



// WithAuth устанавливает пароль для AUTH.
func WithAuth(password string) Option {
	return func(s *Server) {
		s.password = password
	}
}
