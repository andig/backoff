package backoff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"testing"
	"time"
)

type testTimer struct {
	timer *time.Timer
}

func (t *testTimer) Start(duration time.Duration) {
	t.timer = time.NewTimer(0)
}

func (t *testTimer) Stop() {
	if t.timer != nil {
		t.timer.Stop()
	}
}

func (t *testTimer) C() <-chan time.Time {
	return t.timer.C
}

func TestRetry(t *testing.T) {
	const successOn = 3
	var i = 0

	// This function is successful on "successOn" calls.
	f := func() (bool, error) {
		i++
		log.Printf("function is called %d. time\n", i)

		if i == successOn {
			log.Println("OK")
			return true, nil
		}

		log.Println("error")
		return false, errors.New("error")
	}

	_, err := RetryCtx(context.Background(), f, WithBackOff(NewExponentialBackOff()), withTimer(&testTimer{}))
	if err != nil {
		t.Errorf("unexpected error: %s", err.Error())
	}
	if i != successOn {
		t.Errorf("invalid number of retries: %d", i)
	}
}

// TestRetryNoContext covers the context-free wrappers, which must behave like
// their Ctx counterparts called with context.Background().
func TestRetryNoContext(t *testing.T) {
	var i int
	res, err := Retry(func() (int, error) {
		i++
		if i < 3 {
			return 0, errors.New("error")
		}
		return 42, nil
	}, WithBackOff(NewExponentialBackOff()), withTimer(&testTimer{}))
	if err != nil {
		t.Errorf("unexpected error: %s", err)
	}
	if res != 42 {
		t.Errorf("expected 42, got %d", res)
	}

	var j int
	err = RetryError(func() error {
		j++
		return io.EOF
	}, WithMaxTries(2), withTimer(&testTimer{}))
	if !errors.Is(err, ErrExhausted) || !errors.Is(err, io.EOF) {
		t.Errorf("expected ErrExhausted wrapping io.EOF, got: %v", err)
	}
	if j != 2 {
		t.Errorf("expected 2 tries, got %d", j)
	}
}

func TestRetryError(t *testing.T) {
	const successOn = 3
	var i = 0

	// This function is successful on "successOn" calls.
	f := func() error {
		i++
		if i == successOn {
			return nil
		}
		return errors.New("error")
	}

	err := RetryErrorCtx(context.Background(), f, WithBackOff(NewExponentialBackOff()), withTimer(&testTimer{}))
	if err != nil {
		t.Errorf("unexpected error: %s", err.Error())
	}
	if i != successOn {
		t.Errorf("invalid number of retries: %d", i)
	}

	// The operation error must reach the caller unchanged; the rest of the
	// failure semantics is Retry's and is covered by its own tests.
	err = RetryErrorCtx(context.Background(), func() error { return io.EOF },
		WithMaxTries(1), withTimer(&testTimer{}))
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF to be preserved, got: %v", err)
	}
}

func TestRetryWithData(t *testing.T) {
	const successOn = 3
	var i = 0

	// This function is successful on "successOn" calls.
	f := func() (int, error) {
		i++
		log.Printf("function is called %d. time\n", i)

		if i == successOn {
			log.Println("OK")
			return 42, nil
		}

		log.Println("error")
		return 1, errors.New("error")
	}

	res, err := RetryCtx(context.Background(), f, WithBackOff(NewExponentialBackOff()), withTimer(&testTimer{}))
	if err != nil {
		t.Errorf("unexpected error: %s", err.Error())
	}
	if i != successOn {
		t.Errorf("invalid number of retries: %d", i)
	}
	if res != 42 {
		t.Errorf("invalid data in response: %d, expected 42", res)
	}
}

func TestRetryContext(t *testing.T) {
	var cancelOn = 3
	var i = 0

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)

	expectedErr := errors.New("custom error")

	// This function cancels context on "cancelOn" calls.
	f := func() (bool, error) {
		i++
		log.Printf("function is called %d. time\n", i)

		// cancelling the context in the operation function is not a typical
		// use-case, however it allows to get predictable test results.
		if i == cancelOn {
			cancel(expectedErr)
		}

		log.Println("error")
		return false, fmt.Errorf("error (%d)", i)
	}

	_, err := RetryCtx(ctx, f, WithBackOff(NewConstantBackOff(time.Millisecond)), withTimer(&testTimer{}))
	if err == nil {
		t.Errorf("error is unexpectedly nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Errorf("unexpected error: %s", err.Error())
	}
	if i != cancelOn {
		t.Errorf("invalid number of retries: %d", i)
	}
}

// https://github.com/cenkalti/backoff/issues/181
func TestRetryContextErrorIncludesOperationError(t *testing.T) {
	opErr := errors.New("operation error")
	ctxErr := errors.New("context error")

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	i := 0
	f := func() (bool, error) {
		i++
		if i == 2 {
			cancel(ctxErr)
		}
		return false, opErr
	}

	_, err := RetryCtx(ctx, f, WithBackOff(NewConstantBackOff(time.Millisecond)), withTimer(&testTimer{}))
	if !errors.Is(err, ctxErr) {
		t.Errorf("context error not in result: %v", err)
	}
	if !errors.Is(err, opErr) {
		t.Errorf("operation error not in result: %v", err)
	}
}

// https://github.com/cenkalti/backoff/issues/181
func TestRetryMaxElapsedTimeErrorIncludesOperationError(t *testing.T) {
	opErr := errors.New("operation error")

	_, err := RetryCtx(
		context.Background(),
		func() (bool, error) { return false, opErr },
		WithMaxElapsedTime(time.Millisecond),
		withTimer(&testTimer{}),
	)
	if !errors.Is(err, ErrMaxElapsedTime) {
		t.Errorf("ErrMaxElapsedTime not in result: %v", err)
	}
	if !errors.Is(err, opErr) {
		t.Errorf("operation error not in result: %v", err)
	}
}

func TestErrorFields(t *testing.T) {
	opErr := errors.New("operation error")

	// MaxTries reached: Cause is ErrExhausted, LastErr is the operation error.
	_, err := RetryCtx(
		context.Background(),
		func() (bool, error) { return false, opErr },
		WithMaxTries(1),
		withTimer(&testTimer{}),
	)
	if !errors.Is(err, ErrExhausted) {
		t.Errorf("ErrExhausted not in result: %v", err)
	}
	if !errors.Is(err, opErr) {
		t.Errorf("operation error not in result: %v", err)
	}

	// errors.As exposes the structured fields.
	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("result is not an *Error: %v", err)
	}
	if re.LastErr != opErr {
		t.Errorf("LastErr = %v, want %v", re.LastErr, opErr)
	}
	if re.Cause != ErrExhausted {
		t.Errorf("Cause = %v, want %v", re.Cause, ErrExhausted)
	}

	// Backoff policy returning Stop also reports ErrExhausted.
	_, err = RetryCtx(
		context.Background(),
		func() (bool, error) { return false, opErr },
		WithBackOff(&StopBackOff{}),
		withTimer(&testTimer{}),
	)
	if !errors.Is(err, ErrExhausted) {
		t.Errorf("ErrExhausted not in result on Stop: %v", err)
	}
	if !errors.Is(err, opErr) {
		t.Errorf("operation error not in result on Stop: %v", err)
	}
}

func TestRetryPermanent(t *testing.T) {
	ensureRetries := func(test string, shouldRetry bool, f func() (int, error), expectRes int) {
		numRetries := -1
		maxRetries := 1

		res, _ := RetryCtx(
			context.Background(),
			func() (int, error) {
				numRetries++
				if numRetries >= maxRetries {
					return -1, Permanent(errors.New("forced"))
				}
				return f()
			},
			WithBackOff(NewExponentialBackOff()),
			withTimer(&testTimer{}),
		)

		if shouldRetry && numRetries == 0 {
			t.Errorf("Test: '%s', backoff should have retried", test)
		}

		if !shouldRetry && numRetries > 0 {
			t.Errorf("Test: '%s', backoff should not have retried", test)
		}

		if res != expectRes {
			t.Errorf("Test: '%s', got res %d but expected %d", test, res, expectRes)
		}
	}

	for _, testCase := range []struct {
		name        string
		f           func() (int, error)
		shouldRetry bool
		res         int
	}{
		{
			"nil test",
			func() (int, error) {
				return 1, nil
			},
			false,
			1,
		},
		{
			"io.EOF",
			func() (int, error) {
				return 2, io.EOF
			},
			true,
			-1,
		},
		{
			"Permanent(io.EOF)",
			func() (int, error) {
				return 3, Permanent(io.EOF)
			},
			false,
			3,
		},
		{
			"Wrapped: Permanent(io.EOF)",
			func() (int, error) {
				return 4, fmt.Errorf("Wrapped error: %w", Permanent(io.EOF))
			},
			false,
			4,
		},
	} {
		ensureRetries(testCase.name, testCase.shouldRetry, testCase.f, testCase.res)
	}
}

func TestPermanent(t *testing.T) {
	want := errors.New("foo")
	other := errors.New("bar")
	err := Permanent(want)

	if got := errors.Unwrap(err); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
	if !errors.Is(err, want) {
		t.Errorf("err: %v is not %v", err, want)
	}
	if errors.Is(err, other) {
		t.Errorf("err: %v is %v", err, other)
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("err: %v is not ErrPermanent", err)
	}

	// A Permanent error stays detectable through wrapping.
	wrapped := fmt.Errorf("wrapped: %w", err)
	if !errors.Is(wrapped, ErrPermanent) {
		t.Errorf("wrapped: %v is not ErrPermanent", wrapped)
	}
	if !errors.Is(wrapped, want) {
		t.Errorf("wrapped: %v is not %v", wrapped, want)
	}

	if Permanent(nil) != nil {
		t.Errorf("Permanent(nil) should be nil")
	}
}

func TestRetryPermanentError(t *testing.T) {
	opErr := errors.New("operation error")

	_, err := RetryCtx(
		context.Background(),
		func() (bool, error) { return false, Permanent(opErr) },
		withTimer(&testTimer{}),
	)
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("ErrPermanent not in result: %v", err)
	}
	if !errors.Is(err, opErr) {
		t.Errorf("operation error not in result: %v", err)
	}

	var re *Error
	if !errors.As(err, &re) {
		t.Fatalf("result is not an *Error: %v", err)
	}
	if re.Cause != ErrPermanent {
		t.Errorf("Cause = %v, want ErrPermanent", re.Cause)
	}
	if re.LastErr != opErr {
		t.Errorf("LastErr = %v, want %v", re.LastErr, opErr)
	}
}

// Permanent error bubbles up when WithMaxTries(1)
// https://github.com/cenkalti/backoff/issues/177
func TestIssue177(t *testing.T) {
	dummyErr := errors.New("dummy")
	operation := func() (int, error) {
		return 0, Permanent(dummyErr)
	}
	for i := range uint(3) {
		_, err := RetryCtx(context.TODO(), operation, WithMaxTries(i))
		if !errors.Is(err, dummyErr) {
			t.Errorf("unexpected error: %v", err)
		}
		if !errors.Is(err, ErrPermanent) {
			t.Errorf("error is not ErrPermanent: %v", err)
		}
	}
}

// spyTimer records the durations it is asked to wait, then fires immediately.
type spyTimer struct {
	starts []time.Duration
	timer  *time.Timer
}

func (t *spyTimer) Start(d time.Duration) {
	t.starts = append(t.starts, d)
	t.timer = time.NewTimer(0)
}

func (t *spyTimer) Stop() {
	if t.timer != nil {
		t.timer.Stop()
	}
}

func (t *spyTimer) C() <-chan time.Time { return t.timer.C }

// countingBackOff records how many times it is reset and queried.
type countingBackOff struct{ resets, nexts int }

func (b *countingBackOff) Reset() { b.resets++ }

func (b *countingBackOff) NextBackOff() time.Duration {
	b.nexts++
	return time.Second
}

func TestRetryContextDeadline(t *testing.T) {
	// A context deadline surfaces as Cause context.DeadlineExceeded — distinct
	// from WithMaxElapsedTime's ErrMaxElapsedTime — with LastErr preserved.
	opErr := errors.New("operation error")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancel()

	_, err := RetryCtx(ctx, func() (int, error) { return 0, opErr }, withTimer(&testTimer{}))

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Cause is not context.DeadlineExceeded: %v", err)
	}
	if !errors.Is(err, opErr) {
		t.Errorf("operation error not in result: %v", err)
	}
	var re *Error
	if !errors.As(err, &re) || re.Cause != context.DeadlineExceeded {
		t.Errorf("Error.Cause = %v, want context.DeadlineExceeded", err)
	}
}

func TestRetryNotify(t *testing.T) {
	t.Run("fires between attempts with the failing error, not on success", func(t *testing.T) {
		const successOn = 3
		calls := 0
		var got []string
		_, err := RetryCtx(context.Background(),
			func() (int, error) {
				calls++
				if calls == successOn {
					return 1, nil
				}
				return 0, fmt.Errorf("attempt %d", calls)
			},
			WithBackOff(&ZeroBackOff{}), withTimer(&testTimer{}),
			WithNotify(func(e error, _ time.Duration) { got = append(got, e.Error()) }),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0] != "attempt 1" || got[1] != "attempt 2" {
			t.Errorf("Notify errors = %v, want [attempt 1 attempt 2]", got)
		}
	})

	t.Run("not called for the terminal, exhausting error", func(t *testing.T) {
		// Two attempts both fail: Notify fires once (the single retry), not for
		// the second attempt that hits the limit and stops Retry.
		n := 0
		_, err := RetryCtx(context.Background(),
			func() (int, error) { return 0, errors.New("boom") },
			WithMaxTries(2), WithBackOff(&ZeroBackOff{}), withTimer(&testTimer{}),
			WithNotify(func(error, time.Duration) { n++ }),
		)
		if !errors.Is(err, ErrExhausted) {
			t.Fatalf("Cause = %v, want ErrExhausted", err)
		}
		if n != 1 {
			t.Errorf("Notify called %d times, want 1 (only the retry, not the terminal error)", n)
		}
	})

	t.Run("not called on a permanent error", func(t *testing.T) {
		n := 0
		_, _ = RetryCtx(context.Background(),
			func() (int, error) { return 0, Permanent(errors.New("nope")) },
			withTimer(&testTimer{}),
			WithNotify(func(error, time.Duration) { n++ }),
		)
		if n != 0 {
			t.Errorf("Notify called %d times on a permanent error, want 0", n)
		}
	})

	t.Run("not called on context cancellation", func(t *testing.T) {
		n := 0
		ctx, cancel := context.WithCancel(context.Background())
		_, _ = RetryCtx(ctx,
			func() (int, error) { cancel(); return 0, errors.New("boom") },
			WithBackOff(&ZeroBackOff{}), withTimer(&testTimer{}),
			WithNotify(func(error, time.Duration) { n++ }),
		)
		if n != 0 {
			t.Errorf("Notify called %d times on cancellation, want 0", n)
		}
	})
}

func TestRetryAfter(t *testing.T) {
	// A RetryAfterError overrides the next wait with its own duration and
	// resets the backoff policy.
	const retryAfter = 42 * time.Second
	bo := &countingBackOff{}
	tm := &spyTimer{}
	calls := 0

	_, err := RetryCtx(context.Background(),
		func() (int, error) {
			calls++
			if calls == 1 {
				return 0, &RetryAfterError{Duration: retryAfter}
			}
			return 1, nil
		},
		WithBackOff(bo),
		WithMaxElapsedTime(0),
		withTimer(tm),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tm.starts) != 1 || tm.starts[0] != retryAfter {
		t.Errorf("timer waits = %v, want a single wait of %v", tm.starts, retryAfter)
	}
	// Reset is called once at the start of Retry and again because of RetryAfter.
	if bo.resets != 2 {
		t.Errorf("BackOff.Reset called %d times, want 2 (initial + RetryAfter)", bo.resets)
	}
}

func TestRetryAfterError(t *testing.T) {
	// nil cause: behaves like a bare retry-after, Unwrap is nil.
	err := RetryAfter(3*time.Second, nil)
	var ra *RetryAfterError
	if !errors.As(err, &ra) {
		t.Fatalf("RetryAfter did not return a *RetryAfterError: %v", err)
	}
	if ra.Duration != 3*time.Second {
		t.Errorf("Duration = %v, want 3s", ra.Duration)
	}
	if got, want := ra.Error(), "retry after 3s"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if errors.Unwrap(err) != nil {
		t.Errorf("Unwrap() = %v, want nil", errors.Unwrap(err))
	}

	// With a cause: it is wrapped (Unwrap/Is see it) and shown in the message.
	cause := errors.New("rate limited")
	werr := RetryAfter(3*time.Second, cause)
	if !errors.Is(werr, cause) {
		t.Errorf("RetryAfter(_, cause) does not wrap cause: %v", werr)
	}
	if got, want := werr.Error(), "rate limited (retry after 3s)"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// https://github.com/cenkalti/backoff/issues/184
func TestRetryAfterCarriesError(t *testing.T) {
	// When an operation returns RetryAfter with a cause and retrying then stops
	// (here via WithMaxTries), the cause — not the RetryAfterError — is reported
	// as LastErr, so the error context is not discarded.
	cause := errors.New("rate limited")
	_, err := RetryCtx(
		context.Background(),
		func() (int, error) { return 0, RetryAfter(time.Second, cause) },
		WithMaxTries(1),
		withTimer(&testTimer{}),
	)
	if !errors.Is(err, ErrExhausted) {
		t.Errorf("Cause = %v, want ErrExhausted", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("cause not in result: %v", err)
	}
	var re *Error
	if !errors.As(err, &re) || re.LastErr != cause {
		t.Errorf("LastErr = %v, want %v", re, cause)
	}

	// A nil cause keeps the previous behavior: LastErr is the RetryAfterError
	// itself.
	_, err = RetryCtx(
		context.Background(),
		func() (int, error) { return 0, RetryAfter(time.Second, nil) },
		WithMaxTries(1),
		withTimer(&testTimer{}),
	)
	if !errors.As(err, &re) {
		t.Fatalf("result is not an *Error: %v", err)
	} else if _, ok := re.LastErr.(*RetryAfterError); !ok {
		t.Errorf("LastErr = %T, want *RetryAfterError", re.LastErr)
	}
}

func TestErrorString(t *testing.T) {
	// The message is the operation's own error, undecorated by the cause.
	re := &Error{LastErr: errors.New("last"), Cause: ErrExhausted}
	if got, want := re.Error(), "last"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	// A permanent failure reports the error passed to Permanent, with no
	// "backoff: permanent error" prefix.
	_, err := Retry(func() (int, error) {
		return 0, Permanent(errors.New("boom"))
	}, withTimer(&testTimer{}))
	if got, want := err.Error(), "boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	// With no operation error to report, the cause stands in.
	if got, want := (&Error{Cause: ErrExhausted}).Error(), ErrExhausted.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	// Error implements Unwrap() []error, so the single-value errors.Unwrap
	// returns nil; callers must use errors.Is/As. This pins the documented gotcha.
	if got := errors.Unwrap(error(re)); got != nil {
		t.Errorf("errors.Unwrap(Error) = %v, want nil", got)
	}
}
