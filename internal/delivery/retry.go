package delivery

import (
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RetryPolicy struct {
	Base   time.Duration
	Cap    time.Duration
	Jitter func(time.Duration) time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		Base: time.Second,
		Cap:  15 * time.Minute,
		Jitter: func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(max) + 1))
		},
	}
}

func (p RetryPolicy) Delay(attempt int16, serverDelay time.Duration) time.Duration {
	if p.Base <= 0 || p.Cap <= 0 {
		return 0
	}
	ceiling := p.Base
	for current := int16(1); current < attempt && ceiling < p.Cap; current++ {
		if ceiling > p.Cap/2 {
			ceiling = p.Cap
			break
		}
		ceiling *= 2
	}
	if ceiling > p.Cap {
		ceiling = p.Cap
	}
	jitter := p.Jitter
	if jitter == nil {
		jitter = DefaultRetryPolicy().Jitter
	}
	delay := jitter(ceiling)
	if delay < 0 {
		delay = 0
	}
	if delay > ceiling {
		delay = ceiling
	}
	if serverDelay > delay && serverDelay <= p.Cap {
		delay = serverDelay
	}
	return delay
}

func retryAfter(value string, now time.Time, cap time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil {
		if seconds <= 0 || cap <= 0 || seconds > int64(cap/time.Second) {
			return 0
		}
		delay := time.Duration(seconds) * time.Second
		return delay
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	delay := when.Sub(now)
	if delay > cap {
		return 0
	}
	return delay
}

type PollPolicy struct {
	Base   time.Duration
	Cap    time.Duration
	Jitter func(time.Duration) time.Duration
}

func defaultPollPolicy(base time.Duration, jitter func(time.Duration) time.Duration) PollPolicy {
	cap := base * 16
	if cap > 10*time.Second {
		cap = 10 * time.Second
	}
	if jitter == nil {
		jitter = func(span time.Duration) time.Duration {
			if span <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(span) + 1))
		}
	}
	return PollPolicy{Base: base, Cap: cap, Jitter: jitter}
}

func (p PollPolicy) Delay(emptyPolls int16) time.Duration {
	ceiling := p.Base
	for current := int16(1); current < emptyPolls && ceiling < p.Cap; current++ {
		if ceiling > p.Cap/2 {
			ceiling = p.Cap
			break
		}
		ceiling *= 2
	}
	if ceiling > p.Cap {
		ceiling = p.Cap
	}
	floor := ceiling / 2
	jitter := p.Jitter(ceiling - floor)
	if jitter < 0 {
		jitter = 0
	}
	if jitter > ceiling-floor {
		jitter = ceiling - floor
	}
	return floor + jitter
}
