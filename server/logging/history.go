package logging

import (
	"bytes"
	"unicode"

	log "gopkg.in/inconshreveable/log15.v2"
)

func CapitalizeHandler(h log.Handler) log.Handler {
	return log.FuncHandler(func(r *log.Record) error {
		runes := []rune(r.Msg)
		runes[0] = unicode.ToUpper(runes[0])
		r.Msg = string(runes)
		return h.Log(r)
	})
}

type HistoryHandler struct {
	DefaultHandler log.Handler
	History        bytes.Buffer
}

func (h *HistoryHandler) Log(r *log.Record) error {
	h.DefaultHandler.Log(r)
	h.History.Write(log.LogfmtFormat().Format(r))
	return nil
}

func NewHistoryHandler(defaultHandler log.Handler) *HistoryHandler {
	return &HistoryHandler{
		DefaultHandler: defaultHandler,
	}
}

func ToLogLvl(lvlStr string) log.Lvl {
	switch lvlStr {
	case "debug":
		return log.LvlDebug
	case "info":
		return log.LvlInfo
	case "warn":
		return log.LvlWarn
	case "error":
		return log.LvlError
	}
	return log.LvlInfo
}
