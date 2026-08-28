# ai/Makefile — build the C PICS-8 decoder as a shared library for the
# PyTorch adapter. The sources are the repo's own ojph/*.c (paper-benchmarked
# C implementation); no codec code is duplicated here.

UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
  SHLIB   := libmic_pics.dylib
  SHFLAGS := -dynamiclib
else
  SHLIB   := libmic_pics.so
  SHFLAGS := -shared
endif

SRC     := ../ojph/mic_decompress_c.c ../ojph/mic_parallel.c
CFLAGS  ?= -O3 -fPIC

$(SHLIB): $(SRC)
	gcc $(CFLAGS) $(SHFLAGS) -o $@ $(SRC) -lpthread

.PHONY: test clean
test: $(SHLIB)
	../.venv/bin/python -m pytest tests/ -v

clean:
	rm -f $(SHLIB)