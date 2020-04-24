package testing

// Code generated by github.com/launchdarkly/go-options.  DO NOT EDIT.

import (
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ApplyTestCaseOptionFunc func(c *testOptions) error

func (f ApplyTestCaseOptionFunc) apply(c *testOptions) error {
	return f(c)
}

func newTestOptions(options ...TestCaseOption) (testOptions, error) {
	var c testOptions
	err := applyTestOptionsOptions(&c, options...)
	return c, err
}

func applyTestOptionsOptions(c *testOptions, options ...TestCaseOption) error {
	c.ExpectedResult = reconcile.Result{Requeue: true}
	c.AfterFunc = Ignore
	c.Labels = map[string]string{}
	for _, o := range options {
		if err := o.apply(c); err != nil {
			return err
		}
	}
	return nil
}

type TestCaseOption interface {
	apply(*testOptions) error
}

func WithStepName(o string) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.StepName = o
		return nil
	}
}

func WithRequest(o reconcile.Request) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.Request = o
		return nil
	}
}

func WithExpectedResult(o reconcile.Result) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.ExpectedResult = o
		return nil
	}
}

func WithExpectedError(o error) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.ExpectedError = o
		return nil
	}
}

func WithName(o string) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.Name = o
		return nil
	}
}

func WithNamespace(o string) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.Namespace = o
		return nil
	}
}

func WithTestObj(o runtime.Object) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.TestObj = o
		return nil
	}
}

func WithAfter(o ReconcilerTestValidationFunc) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.AfterFunc = o
		return nil
	}
}

func WithLabels(o map[string]string) ApplyTestCaseOptionFunc {
	return func(c *testOptions) error {
		c.Labels = o
		return nil
	}
}