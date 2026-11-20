# Shared gates come from the published go-standards; see standard.mk.
include standard.mk

CHECK_EXTRA := modverify

.PHONY: modverify fuzz
modverify:
	$(GO) mod verify

FUZZTIME ?= 30s

fuzz:
	$(GO) test -run '^$$' -fuzz FuzzNew -fuzztime $(FUZZTIME) ./parse
	$(GO) test -run '^$$' -fuzz FuzzParse -fuzztime $(FUZZTIME) ./config
	$(GO) test -run '^$$' -fuzz FuzzLines -fuzztime $(FUZZTIME) ./diff
	$(GO) test -run '^$$' -fuzz FuzzParseSelector -fuzztime $(FUZZTIME) ./device
	$(GO) test -run '^$$' -fuzz FuzzDecode -fuzztime $(FUZZTIME) ./change
	$(GO) test -run '^$$' -fuzz FuzzDecodePolicy -fuzztime $(FUZZTIME) ./assess
