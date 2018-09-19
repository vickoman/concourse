// Code generated by counterfeiter. DO NOT EDIT.
package accessorfakes

import (
	"net/http"
	"sync"

	"github.com/concourse/concourse/atc/api/accessor"
)

type FakeAccessFactory struct {
	CreateStub        func(*http.Request, string) accessor.Access
	createMutex       sync.RWMutex
	createArgsForCall []struct {
		arg1 *http.Request
		arg2 string
	}
	createReturns struct {
		result1 accessor.Access
	}
	createReturnsOnCall map[int]struct {
		result1 accessor.Access
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *FakeAccessFactory) Create(arg1 *http.Request, arg2 string) accessor.Access {
	fake.createMutex.Lock()
	ret, specificReturn := fake.createReturnsOnCall[len(fake.createArgsForCall)]
	fake.createArgsForCall = append(fake.createArgsForCall, struct {
		arg1 *http.Request
		arg2 string
	}{arg1, arg2})
	fake.recordInvocation("Create", []interface{}{arg1, arg2})
	fake.createMutex.Unlock()
	if fake.CreateStub != nil {
		return fake.CreateStub(arg1, arg2)
	}
	if specificReturn {
		return ret.result1
	}
	return fake.createReturns.result1
}

func (fake *FakeAccessFactory) CreateCallCount() int {
	fake.createMutex.RLock()
	defer fake.createMutex.RUnlock()
	return len(fake.createArgsForCall)
}

func (fake *FakeAccessFactory) CreateArgsForCall(i int) (*http.Request, string) {
	fake.createMutex.RLock()
	defer fake.createMutex.RUnlock()
	return fake.createArgsForCall[i].arg1, fake.createArgsForCall[i].arg2
}

func (fake *FakeAccessFactory) CreateReturns(result1 accessor.Access) {
	fake.CreateStub = nil
	fake.createReturns = struct {
		result1 accessor.Access
	}{result1}
}

func (fake *FakeAccessFactory) CreateReturnsOnCall(i int, result1 accessor.Access) {
	fake.CreateStub = nil
	if fake.createReturnsOnCall == nil {
		fake.createReturnsOnCall = make(map[int]struct {
			result1 accessor.Access
		})
	}
	fake.createReturnsOnCall[i] = struct {
		result1 accessor.Access
	}{result1}
}

func (fake *FakeAccessFactory) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.createMutex.RLock()
	defer fake.createMutex.RUnlock()
	copiedInvocations := map[string][][]interface{}{}
	for key, value := range fake.invocations {
		copiedInvocations[key] = value
	}
	return copiedInvocations
}

func (fake *FakeAccessFactory) recordInvocation(key string, args []interface{}) {
	fake.invocationsMutex.Lock()
	defer fake.invocationsMutex.Unlock()
	if fake.invocations == nil {
		fake.invocations = map[string][][]interface{}{}
	}
	if fake.invocations[key] == nil {
		fake.invocations[key] = [][]interface{}{}
	}
	fake.invocations[key] = append(fake.invocations[key], args)
}

var _ accessor.AccessFactory = new(FakeAccessFactory)