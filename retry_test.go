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

	_, err := Retry(context.Background(), f, WithBackOff(NewExponentialBackOff()), withTimer(&testTimer{}))
	if err != nil {
		t.Errorf("unexpected error: %s", err.Error())
	}
	if i != successOn {
		t.Errorf("invalid number of retries: %d", i)
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

	res, err := Retry(context.Background(), f, WithBackOff(NewExponentialBackOff()), withTimer(&testTimer{}))
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

	_, err := Retry(ctx, f, WithBackOff(NewConstantBackOff(time.Millisecond)), withTimer(&testTimer{}))
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

	_, err := Retry(ctx, f, WithBackOff(NewConstantBackOff(time.Millisecond)), withTimer(&testTimer{}))
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

	_, err := Retry(
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

func TestRetryError(t *testing.T) {
	opErr := errors.New("operation error")

	// MaxTries reached: Cause is ErrExhausted, LastErr is the operation error.
	_, err := Retry(
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
	var re *RetryError
	if !errors.As(err, &re) {
		t.Fatalf("result is not a *RetryError: %v", err)
	}
	if re.LastErr != opErr {
		t.Errorf("LastErr = %v, want %v", re.LastErr, opErr)
	}
	if re.Cause != ErrExhausted {
		t.Errorf("Cause = %v, want %v", re.Cause, ErrExhausted)
	}

	// Backoff policy returning Stop also reports ErrExhausted.
	_, err = Retry(
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

		res, _ := Retry(
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

	_, err := Retry(
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

	re := AsRetryError(err)
	if re == nil {
		t.Fatalf("result is not a *RetryError: %v", err)
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
		_, err := Retry(context.TODO(), operation, WithMaxTries(i))
		if !errors.Is(err, dummyErr) {
			t.Errorf("unexpected error: %v", err)
		}
		if !errors.Is(err, ErrPermanent) {
			t.Errorf("error is not ErrPermanent: %v", err)
		}
	}
}
