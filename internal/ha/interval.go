package ha

import "time"

const (
	targetInterval = time.Minute
	intervalMargin = 10 * time.Second
	alertAfter     = 5 * time.Minute
)

func NextInterval(elapsed time.Duration) time.Duration {
	if elapsed <= targetInterval {
		return targetInterval
	}
	return elapsed + intervalMargin
}

func SyncAlert(elapsed time.Duration) string {
	if elapsed > alertAfter {
		return "文件同步一轮超过 5 分钟，请考虑拆分站点或稍后启用 inotify"
	}
	return ""
}

func ShouldDegrade(localRole string, localTerm int, peerReachable bool, peerRole string, peerTerm int) bool {
	if !peerReachable {
		return false
	}
	if localRole != RoleMaster {
		return false
	}
	return peerRole == RoleMaster && peerTerm > localTerm
}
