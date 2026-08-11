package version

import (
	"reflect"
	"testing"
)

func TestEmbeddedConfigsMatchSourceAndReturnDeepCopies(t *testing.T) {
	source, err := LoadDir("../../configs/addresses")
	if err != nil {
		t.Fatal(err)
	}
	embedded := EmbeddedConfigs()
	if !reflect.DeepEqual(embedded, source) {
		t.Fatal("embedded address configurations differ from configs/addresses")
	}

	config := embedded[25297]
	config.SceneOffsets[0]++
	embedded[25297] = config
	fresh := EmbeddedConfigs()[25297]
	if fresh.SceneOffsets[0] != source[25297].SceneOffsets[0] {
		t.Fatal("EmbeddedConfigs returned shared mutable data")
	}
}
