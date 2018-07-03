package matchers

import (
	"reflect"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/petergtz/pegomock"
)

func AnyPtrToLoggingSimpleLogger() log.Logger {
	pegomock.RegisterMatcher(pegomock.NewAnyMatcher(reflect.TypeOf((*(log.Logger))(nil)).Elem()))
	var nullValue log.Logger
	return nullValue
}

func EqPtrToLoggingSimpleLogger(value log.Logger) log.Logger {
	pegomock.RegisterMatcher(&pegomock.EqMatcher{Value: value})
	var nullValue log.Logger
	return nullValue
}
