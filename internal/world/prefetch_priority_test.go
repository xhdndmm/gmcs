//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package world

import "testing"

func TestNextPrefetchPrioritizesVisibleChunks(t *testing.T) {
	w := &World{
		priorityQueue:   make(chan ChunkPos, 1),
		genQueue:        make(chan ChunkPos, 1),
		priorityPending: make(map[ChunkPos]struct{}),
		quit:            make(chan struct{}),
	}
	low := ChunkPos{X: 10, Z: 20}
	high := ChunkPos{X: 1, Z: 2}
	w.genQueue <- low
	w.priorityQueue <- high
	got, ok := w.nextPrefetch()
	if !ok || got != high {
		t.Fatalf("nextPrefetch = %+v/%v, want high-priority %+v", got, ok, high)
	}
	got, ok = w.nextPrefetch()
	if !ok || got != low {
		t.Fatalf("nextPrefetch = %+v/%v, want low-priority %+v", got, ok, low)
	}
}
