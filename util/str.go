package util

import "strings"

// Includes는 문자열 s가 부분 문자열 substr을 포함하는지 확인합니다.
func Includes(s, substr string) bool {
	if substr == "" {
		return true
	}
	if s == "" {
		return false
	}
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func IncludesIgnoreCase(s, substr string) bool {
	if substr == "" {
		return true
	}
	if s == "" {
		return false
	}
	if len(substr) > len(s) {
		return false
	}

	// 대소문자 구분 없이 비교하기 위해 소문자로 변환
	sLower := strings.ToLower(s)
	substrLower := strings.ToLower(substr)

	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return true
		}
	}
	return false
}

// normalizeAddr normalizes the address to a consistent format (e.g., replace [::1] with localhost)
func NormalizeAddr(addr string) string {
	// IPv6 루프백 주소를 localhost로 변환
	addr = strings.Replace(addr, "[::1]", "localhost", 1)
	// 추가적인 정규화가 필요하면 여기에 구현 (예: 포트 번호 정리)
	return addr
}
