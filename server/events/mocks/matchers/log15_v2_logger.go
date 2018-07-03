package matchers

import (
	"reflect"

	"github.com/petergtz/pegomock"
	log15_v2 "gopkg.in/inconshreveable/log15.v2"
)

func AnyLog15V2Logger() log15_v2.Logger {
	pegomock.RegisterMatcher(pegomock.NewAnyMatcher(reflect.TypeOf((*(log15_v2.Logger))(nil)).Elem()))
	var nullValue log15_v2.Logger
	return nullValue
}

func EqLog15V2Logger(value log15_v2.Logger) log15_v2.Logger {
	pegomock.RegisterMatcher(&pegomock.EqMatcher{Value: value})
	var nullValue log15_v2.Logger
	return nullValue
}
