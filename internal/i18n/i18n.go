// SPDX-License-Identifier: GPL-3.0-or-later

// Package i18n picks the German or English wording of a message. Each
// message is written in both languages where it is used, T("…", "…"), so
// there is no catalogue to keep in step with the code.
package i18n

import (
	"fmt"
	"strings"
	"sync/atomic"
)

var english atomic.Bool

// Photos is "1 photo" or "n photos" in the current language.
func Photos(n int) string {
	if n == 1 {
		return T("1 Bild", "1 photo")
	}
	return fmt.Sprintf(T("%d Bilder", "%d photos"), n)
}

// SetLanguage switches messages to "en"; anything else means German.
func SetLanguage(lang string) { english.Store(lang == "en") }

// Language is "de" or "en".
func Language() string {
	if english.Load() {
		return "en"
	}
	return "de"
}

// T returns the wording for the current language.
func T(de, en string) string {
	if english.Load() {
		return en
	}
	return de
}

// Resolve turns a stored choice into a language: "de" and "en" stand, ""
// follows the system, where German means German and anything else English.
func Resolve(choice string) string {
	switch choice {
	case "de", "en":
		return choice
	}
	if strings.HasPrefix(strings.ToLower(systemLanguage()), "de") {
		return "de"
	}
	return "en"
}
