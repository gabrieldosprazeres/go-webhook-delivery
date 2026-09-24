package chaoslab

func (s *state) increment(scenario string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counters[scenario]++
	return s.counters[scenario]
}

func (s *state) recordStatus(scenario string, status int) {
	s.mu.Lock()
	s.lastScenario, s.lastStatus, s.validSignatures = scenario, status, 0
	s.mu.Unlock()
}

func (s *state) record(scenario string, status, valid int) {
	s.mu.Lock()
	s.counters[scenario]++
	s.lastScenario, s.lastStatus, s.validSignatures = scenario, status, valid
	s.mu.Unlock()
}
