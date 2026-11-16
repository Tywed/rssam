package httpserver

import (
	"net/http"

	"rssam/internal/http/middleware"
)

func sameOriginRequest(r *http.Request) bool { return middleware.SameOriginRequest(r) }

func isSafeMethod(method string) bool { return middleware.IsSafeMethod(method) }
