package ui

import (
	"errors"
	"net/http"
	"net/url"

	"rssam/internal/reader"
	"rssam/internal/requestid"
	"rssam/internal/service"
)

// Flash codes carried in the redirect query; baseData turns them into the
// error banner. A mutation that failed must never answer with the same
// redirect as one that succeeded.
const (
	flashErrParam    = "err"
	flashErrRequest  = "rid"
	flashErrInternal = "op"
	flashErrFetch    = "fetch"
	flashErrStale    = "stale"
)

// failRedirect logs err under the request id and sends the browser to
// target with the matching flash code.
func (h *Handler) failRedirect(w http.ResponseWriter, r *http.Request, target, action string, err error) {
	code := flashErrInternal
	if _, rateLimited := reader.RetryAt(err); rateLimited || errors.Is(err, service.ErrFetchFeed) {
		code = flashErrFetch
		h.log.WarnContext(r.Context(), action+" failed", "err", err)
	} else {
		h.log.ErrorContext(r.Context(), action+" failed", "err", err)
	}
	http.Redirect(w, r, withFlashErr(target, code, requestid.FromContext(r.Context())), http.StatusFound)
}

// csrfRejected answers a form whose token did not validate: the page was
// opened before a restart, in another tab after re-login, or sat for
// longer than the token lives. Nothing was changed, so the browser goes
// back to the page it came from (same-origin Referer, else the feed list)
// with a banner instead of a bare 403. The login form keeps its own path
// (no session, nonce rotation), as do the JSON endpoints.
func (h *Handler) csrfRejected(w http.ResponseWriter, r *http.Request) {
	h.log.InfoContext(r.Context(), "ui form rejected: stale csrf token", "path", r.URL.Path)
	http.Redirect(w, r, withFlashErr(refererOr(r, "/ui/feeds"), flashErrStale, ""), http.StatusSeeOther)
}

func withFlashErr(target, code, rid string) string {
	u, err := url.Parse(target)
	if err != nil {
		u = &url.URL{Path: "/ui/unread"}
	}
	q := u.Query()
	q.Set(flashErrParam, code)
	if rid != "" {
		q.Set(flashErrRequest, rid)
	}
	u.RawQuery = q.Encode()
	return u.RequestURI()
}

func flashErrText(code, rid string) string {
	switch code {
	case flashErrInternal:
		msg := "Действие не выполнено: внутренняя ошибка."
		if requestid.Valid(rid) {
			msg += " Код запроса " + rid + " — по нему ошибка находится в журнале."
		}
		return msg
	case flashErrStale:
		return "Форма устарела: страница была открыта до перезапуска сервера или слишком долго. Ничего не изменено — обновите страницу и повторите."
	case flashErrFetch:
		return "Ленту не удалось получить: источник недоступен, ограничил частоту или отдал ошибку. Подробности — в карточке ленты."
	default:
		return ""
	}
}
