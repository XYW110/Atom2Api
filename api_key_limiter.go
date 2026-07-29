package main

import (
	"sync"
	"time"
)

const apiKeyRPMWindow = time.Minute

type apiKeyLimitState struct {
	requests []time.Time
	active   int
}

type apiKeyLimitRejection struct {
	retryAfter int
	message    string
}

type apiKeyLimiter struct {
	mu     sync.Mutex
	states map[string]*apiKeyLimitState
	now    func() time.Time
}

func newAPIKeyLimiter() *apiKeyLimiter {
	return &apiKeyLimiter{states: map[string]*apiKeyLimitState{}, now: time.Now}
}

func (l *apiKeyLimiter) acquire(key APIKey) (func(), apiKeyLimitRejection, bool) {
	now := l.now()
	l.mu.Lock()
	state := l.states[key.ID]
	if state == nil {
		state = &apiKeyLimitState{}
		l.states[key.ID] = state
	}

	cutoff := now.Add(-apiKeyRPMWindow)
	firstCurrent := 0
	for firstCurrent < len(state.requests) && !state.requests[firstCurrent].After(cutoff) {
		firstCurrent++
	}
	if firstCurrent > 0 {
		state.requests = append(state.requests[:0], state.requests[firstCurrent:]...)
	}
	if key.RPMLimit == 0 {
		state.requests = nil
	}

	if key.ConcurrencyLimit > 0 && state.active >= key.ConcurrencyLimit {
		l.mu.Unlock()
		return nil, apiKeyLimitRejection{retryAfter: 1, message: "API key concurrency limit exceeded"}, false
	}
	if key.RPMLimit > 0 && len(state.requests) >= key.RPMLimit {
		remaining := state.requests[0].Add(apiKeyRPMWindow).Sub(now)
		retryAfter := int((remaining + time.Second - 1) / time.Second)
		if retryAfter < 1 {
			retryAfter = 1
		}
		l.mu.Unlock()
		return nil, apiKeyLimitRejection{retryAfter: retryAfter, message: "API key RPM limit exceeded"}, false
	}

	if key.RPMLimit > 0 {
		state.requests = append(state.requests, now)
	}
	state.active++
	l.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			l.mu.Lock()
			if state.active > 0 {
				state.active--
			}
			if state.active == 0 && len(state.requests) == 0 && l.states[key.ID] == state {
				delete(l.states, key.ID)
			}
			l.mu.Unlock()
		})
	}
	return release, apiKeyLimitRejection{}, true
}

func (l *apiKeyLimiter) forget(keyID string) {
	l.mu.Lock()
	delete(l.states, keyID)
	l.mu.Unlock()
}
