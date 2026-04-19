package memory_test

import (
	"testing"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/memtest"
)

func TestInMem_StoreContract(t *testing.T) {
	memtest.RunStoreContract(t, func() memory.Store {
		return memory.NewInMem()
	})
}
