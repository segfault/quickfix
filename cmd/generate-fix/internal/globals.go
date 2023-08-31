package internal

import (
	"fmt"
	"sort"
	"sync"

	"github.com/quickfixgo/quickfix/datadictionary"
)

type fieldTypeMap map[string]*datadictionary.FieldType
type usageMap struct {
	Mutex sync.RWMutex
	Map   map[int]bool
}

var (
	globalFieldTypesLookup fieldTypeMap
	GlobalFieldTypes       []*datadictionary.FieldType
	globalFieldUsage       map[string]*usageMap
	globalFieldUsageMutex  sync.RWMutex
)

// Sort fieldtypes by name.
type byFieldName []*datadictionary.FieldType

func (n byFieldName) Len() int           { return len(n) }
func (n byFieldName) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }
func (n byFieldName) Less(i, j int) bool { return n[i].Name() < n[j].Name() }

type byFieldTag []*datadictionary.FieldType

func (n byFieldTag) Len() int           { return len(n) }
func (n byFieldTag) Swap(i, j int)      { n[i], n[j] = n[j], n[i] }
func (n byFieldTag) Less(i, j int) bool { return n[i].Tag() < n[j].Tag() }

type byFieldTagThenName []*datadictionary.FieldType

func (n byFieldTagThenName) Len() int      { return len(n) }
func (n byFieldTagThenName) Swap(i, j int) { n[i], n[j] = n[j], n[i] }
func (n byFieldTagThenName) Less(i, j int) bool {
	if n[i].Tag() == n[j].Tag() {
		return n[i].Name() < n[j].Name()
	}
	return n[i].Tag() < n[j].Tag()
}

func getGlobalFieldType(f *datadictionary.FieldDef) (t *datadictionary.FieldType, err error) {
	var ok bool
	t, ok = globalFieldTypesLookup[f.Name()]
	if !ok {
		err = fmt.Errorf("Unknown global type for %v", f.Name())
	}

	return
}

func ClearGlobalFieldUsage() {
	fmt.Println("New Global field usage map")
	globalFieldUsage = make(map[string]*usageMap)
}

func isFieldUsageOk(msgKey string, f *datadictionary.FieldDef) bool {
	globalFieldUsageMutex.RLock()
	fieldMap, existing := globalFieldUsage[msgKey]
	globalFieldUsageMutex.RUnlock()
	if !existing {
		fmt.Printf("New Global field usage map for %s\n", msgKey)
		globalFieldUsageMutex.Lock()
		defer globalFieldUsageMutex.Unlock()
		fieldMap = &usageMap{Map: make(map[int]bool)}
		globalFieldUsage[msgKey] = fieldMap
	}

	fieldMap.Mutex.RLock()
	_, alreadyProcessed := fieldMap.Map[f.Tag()]
	fieldMap.Mutex.RUnlock()

	if alreadyProcessed {
		fmt.Printf("Skipping template %s for tag %d %s\n", msgKey, f.Tag(), f.Name())
		return false
	}

	fmt.Printf("Generated template %s for tag %d %s\n", msgKey, f.Tag(), f.Name())
	fieldMap.Mutex.Lock()
	fieldMap.Map[f.Tag()] = true
	fieldMap.Mutex.Unlock()

	return true
}

func BuildGlobalFieldTypes(specs []*datadictionary.DataDictionary) {
	globalFieldTypesLookup = make(fieldTypeMap)
	for _, spec := range specs {
		for _, field := range spec.FieldTypeByTag {
			if oldField, ok := globalFieldTypesLookup[field.Name()]; ok {
				fmt.Printf("MERGING field %s [Tag %d] to the global field type lookup table [src: %p]\n", field.Name(), field.Tag(), spec)
				// Merge old enums with new.
				if len(oldField.Enums) > 0 && field.Enums == nil {
					field.Enums = make(map[string]datadictionary.Enum)
				}

				for enumVal, enum := range oldField.Enums {
					if _, ok := field.Enums[enumVal]; !ok {
						// Verify an existing enum doesn't have the same description. Keep newer enum.
						okToKeepEnum := true
						for _, newEnum := range field.Enums {
							if newEnum.Description == enum.Description {
								okToKeepEnum = false
								break
							}
						}

						if okToKeepEnum {
							field.Enums[enumVal] = enum
						}
					}
				}
			}

			fmt.Printf("Adding field %s [Tag %d] to the global field type lookup table [src: %p]\n", field.Name(), field.Tag(), spec)
			globalFieldTypesLookup[field.Name()] = field
		}

	}

	GlobalFieldTypes = make([]*datadictionary.FieldType, len(globalFieldTypesLookup))
	i := 0
	for _, fieldType := range globalFieldTypesLookup {
		GlobalFieldTypes[i] = fieldType
		i++
	}

	sort.Sort(byFieldTag(GlobalFieldTypes))

	for _, spec := range specs {
		for fieldName, field := range spec.FieldTypeByName {
			if _, found := globalFieldTypesLookup[fieldName]; !found {
				fmt.Printf("Adding field alias %s [Tag %d] to the global field type lookup table [src: %p]\n", fieldName, field.Tag(), spec)
				globalFieldTypesLookup[fieldName] = field
			}
		}
	}
}
