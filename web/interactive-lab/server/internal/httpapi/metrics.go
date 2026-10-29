package httpapi

// Subscribers reports how many browser tabs are connected to the event
// stream right now. It exists so the metrics endpoint can publish it.
func (s *Server) Subscribers() int { return s.hub.count() }
