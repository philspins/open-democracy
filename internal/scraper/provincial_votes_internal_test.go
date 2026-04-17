package scraper

import "testing"

func TestHasPDFTextShowOperator(t *testing.T) {
	if !hasPDFTextShowOperator("BT /F9 7.999 Tf 0 0 0 rg 380.167 TL 242.496 325.155 Td (Kaeding, ) Tj T* ET") {
		t.Fatal("expected line with inline Tj operator to be detected")
	}
	if hasPDFTextShowOperator("q 16.622 368.075 754.532 14.0 re W n") {
		t.Fatal("expected non-text operator line to be ignored")
	}
}

func TestIsPEICaptchaBody_CaseInsensitive(t *testing.T) {
	if !isPEICaptchaBody([]byte(`<html><head><link href="HTTPS://CAPTCHA.PERFDRIVE.COM/challenge.css"></head></html>`)) {
		t.Fatal("expected captcha signature to be detected case-insensitively")
	}
	if !isPEICaptchaBody([]byte(`<script src="https://cdn.perfdrive.com/aperture/aperture.js"></script>`)) {
		t.Fatal("expected generic perfdrive bot-manager signature to be detected")
	}
}
