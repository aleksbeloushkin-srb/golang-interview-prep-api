// Package warm exists only to fill the runner image's build cache: building
// this test with -race compiles every package the practice tasks import, so a
// cold Lambda does not have to.
package warm

import (
	_ "bufio"
	_ "bytes"
	_ "cmp"
	_ "context"
	_ "encoding/json"
	_ "errors"
	_ "fmt"
	_ "io"
	_ "math/rand/v2"
	_ "net"
	_ "net/http"
	_ "net/http/httptest"
	_ "os"
	_ "runtime"
	_ "runtime/pprof"
	_ "slices"
	_ "sort"
	_ "strconv"
	_ "strings"
	_ "sync"
	_ "sync/atomic"
	"testing"
	_ "time"
	_ "unicode/utf8"

	_ "golang.org/x/sync/errgroup"
)

func TestWarm(t *testing.T) {}
