// SPDX-License-Identifier: GPL-3.0-or-later

package i18n

import "testing"

func TestT(t *testing.T) {
	defer SetLanguage("de")
	SetLanguage("en")
	if got := T("Hallo", "Hello"); got != "Hello" || Language() != "en" {
		t.Errorf("en: %q %q", got, Language())
	}
	SetLanguage("de")
	if got := T("Hallo", "Hello"); got != "Hallo" || Language() != "de" {
		t.Errorf("de: %q %q", got, Language())
	}
	if Resolve("en") != "en" || Resolve("de") != "de" {
		t.Error("an explicit choice must stand")
	}
	if r := Resolve(""); r != "de" && r != "en" {
		t.Errorf("system language resolved to %q", r)
	}
}
