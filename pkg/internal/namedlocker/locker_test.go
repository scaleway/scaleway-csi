// Vendored from oya.to/namedlocker v1.0.0 (MIT License, Copyright (c) 2020 oyato cloud)
package namedlocker

import (
	"math/rand"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

func Example() {
	sto := Store{}
	sto.Lock("my-key")
	defer sto.Unlock("my-key")

	// do some work...
}

func TestStore(t *testing.T) {
	sto := Store{}
	testSync := func(wg *sync.WaitGroup, key string) {
		defer wg.Done()

		if err := sto.TryUnlock(key); err != ErrUnlockOfUnlockedKey {
			t.Fatalf("TryUnlock of unlocked key should return ErrUnlockOfUnlockedKey, not %#v\n", err)
		}

		sto.Lock(key)
		if err := sto.TryUnlock(key); err != nil {
			t.Fatalf("TryUnlock of locked key should return nil, not %#v\n", err)
		}

		if err := sto.TryUnlock(key); err != ErrUnlockOfUnlockedKey {
			t.Fatalf("TryUnlock of unlocked key should return ErrUnlockOfUnlockedKey, not %#v\n", err)
		}

		(func() {
			defer func() {
				if err := recover(); err != ErrUnlockOfUnlockedKey {
					t.Fatalf("Unlock of unlocked key should panic with ErrUnlockOfUnlockedKey, not %#v\n", err)
				}
			}()
			sto.Unlock(key)
		})()
		(func() {
			defer func() {
				if err := recover(); err != nil {
					t.Fatalf("Unlock of locked key should not panic with %#v\n", err)
				}
			}()
			sto.Lock(key)
			sto.Unlock(key)
		})()
	}
	testAsync := func(wg *sync.WaitGroup, key string) {
		defer wg.Done()

		sto.Lock(key)
		runtime.Gosched()
		sto.Unlock(key)
	}
	rnd := rand.New(rand.NewSource(0))
	wg := &sync.WaitGroup{}
	for i := 0; i < 100000; i++ {
		wg.Add(1)
		go testSync(wg, strconv.Itoa(rnd.Int()))
	}
	wg.Wait()
	for i := 0; i < 100; i++ {
		key := strconv.Itoa(rnd.Int())
		for j := 0; j < 1000; j++ {
			wg.Add(1)
			go testAsync(wg, key)
		}
	}
	wg.Wait()
	if n := len(sto.refs); n != 0 {
		t.Fatalf("all keys should be unlocked, but Store still contains %d locks\n", n)
	}
}

func BenchmarkSync(b *testing.B) {
	b.ReportAllocs()
	sto := Store{}
	k := ""
	for i := 0; i < b.N; i++ {
		sto.Lock(k)
		runtime.Gosched()
		sto.Unlock(k)
	}
}

func BenchmarkAsync(b *testing.B) {
	b.ReportAllocs()
	sto := Store{}
	k := ""
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sto.Lock(k)
			runtime.Gosched()
			sto.Unlock(k)
		}
	})
}
