package monitorsdk

// Log levels accepted by LogError, the only list in the SDK (ADR-0015). The
// remote's buffer schema checks the same values; TestLevelsMatchTheBufferSchema
// fails if the two diverge.
const (
	LevelInfo     = "INFO"
	LevelWarn     = "WARN"
	LevelError    = "ERROR"
	LevelCritical = "CRITICAL"
	LevelPanic    = "PANIC"
)

// Levels lists every level, from the least to the most severe.
var Levels = []string{LevelInfo, LevelWarn, LevelError, LevelCritical, LevelPanic}

func validLevel(level string) bool {
	for _, known := range Levels {
		if level == known {
			return true
		}
	}
	return false
}
