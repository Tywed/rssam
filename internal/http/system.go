package httpserver

import (
	"runtime"

	"rssam/internal/version"
)

func systemInfoFromBuild(usersCount int) systemInfoDTO {
	return systemInfoDTO{
		Version:    version.Version,
		GoVersion:  runtime.Version(),
		BuildDate:  version.Date,
		Arch:       runtime.GOARCH,
		OS:         runtime.GOOS,
		UsersCount: usersCount,
	}
}
