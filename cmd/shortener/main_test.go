package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildInfo проверяет форматирование сборочной информации: точный вид
// всех трёх строк и подстановку "N/A" вместо пустых значений — признак
// сборки без заливки через -ldflags.
//
// Глобальные переменные buildVersion/buildDate/buildCommit сохраняются
// перед заменой и восстанавливаются после: тест не зависит ни от порядка
// запуска, ни от значений, потенциально залитых флагами линковщика.
func TestBuildInfo(t *testing.T) {
	tests := []struct {
		name    string
		version string
		date    string
		commit  string
		want    string
	}{
		{
			name:    "все значения отсутствуют — сборка без -ldflags",
			version: "",
			date:    "",
			commit:  "",
			want:    "Build version: N/A\nBuild date: N/A\nBuild commit: N/A",
		},
		{
			name:    "все значения заданы",
			version: "v1.2.3",
			date:    "2026-11-12T10:00:00Z",
			commit:  "a1b2c3d",
			want:    "Build version: v1.2.3\nBuild date: 2026-11-12T10:00:00Z\nBuild commit: a1b2c3d",
		},
		{
			name:    "задана только версия",
			version: "v1.2.3-3-g9f8e7d6.dirty",
			date:    "",
			commit:  "",
			want:    "Build version: v1.2.3-3-g9f8e7d6.dirty\nBuild date: N/A\nBuild commit: N/A",
		},
		{
			name:    "заданы дата и коммит, версия пуста",
			version: "",
			date:    "2026-11-12T10:00:00Z",
			commit:  "a1b2c3d",
			want:    "Build version: N/A\nBuild date: 2026-11-12T10:00:00Z\nBuild commit: a1b2c3d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldVersion, oldDate, oldCommit := buildVersion, buildDate, buildCommit
			defer func() { buildVersion, buildDate, buildCommit = oldVersion, oldDate, oldCommit }()

			buildVersion, buildDate, buildCommit = tt.version, tt.date, tt.commit

			assert.Equal(t, tt.want, buildInfo())
		})
	}
}

// TestOrNA проверяет замену пустой строки на "N/A": непустые значения
// проходят без изменений, пустые и отсутствующие заменяются.
func TestOrNA(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "пустая строка заменяется",
			value: "",
			want:  "N/A",
		},
		{
			name:  "непустая строка проходит как есть",
			value: "v1.2.3",
			want:  "v1.2.3",
		},
		{
			name:  "строка из пробелов не пустая и не заменяется",
			value: " ",
			want:  " ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, orNA(tt.value))
		})
	}
}
