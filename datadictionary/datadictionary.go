// Package datadictionary provides support for parsing and organizing FIX Data Dictionaries
package datadictionary

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"

	"github.com/pkg/errors"
)

// DataDictionary models FIX messages, components, and fields.
type DataDictionary struct {
	FIXType         string
	Major           int
	Minor           int
	ServicePack     int
	FieldTypeByTag  map[int]*FieldType
	FieldTypeByName map[string]*FieldType
	Messages        map[string]*MessageDef
	ComponentTypes  map[string]*ComponentType
	Header          *MessageDef
	Trailer         *MessageDef
}

// MessagePart can represent a Field, Repeating Group, or Component.
type MessagePart interface {
	Name() string
	Required() bool
}

// messagePartWithFields is a MessagePart with multiple Fields.
type messagePartWithFields interface {
	MessagePart
	Fields() []*FieldDef
	RequiredFields() []*FieldDef
}

// ComponentType is a grouping of fields.
type ComponentType struct {
	name           string
	parts          []MessagePart
	fields         []*FieldDef
	requiredFields []*FieldDef
	requiredParts  []MessagePart
}

// NewComponentType returns an initialized component type.
func NewComponentType(name string, parts []MessagePart) *ComponentType {
	comp := ComponentType{
		name:  name,
		parts: parts,
	}

	for _, part := range parts {

		if part.Required() {
			comp.requiredParts = append(comp.requiredParts, part)
		}

		switch f := part.(type) {
		case messagePartWithFields:
			comp.fields = append(comp.fields, f.Fields()...)

			if f.Required() {
				comp.requiredFields = append(comp.requiredFields, f.RequiredFields()...)
			}
		case *FieldDef:
			comp.fields = append(comp.fields, f)

			if f.Required() {
				comp.requiredFields = append(comp.requiredFields, f)
			}
		}
	}

	return &comp
}

// Name returns the name of this component type.
func (c ComponentType) Name() string { return c.name }

// Fields returns all fields contained in this component. Includes fields
// encapsulated in components of this component.
func (c ComponentType) Fields() []*FieldDef { return c.fields }

// RequiredFields returns those fields that are required for this component.
func (c ComponentType) RequiredFields() []*FieldDef { return c.requiredFields }

// RequiredParts returns those parts that are required for this component.
func (c ComponentType) RequiredParts() []MessagePart { return c.requiredParts }

// Parts returns all parts in declaration order contained in this component.
func (c ComponentType) Parts() []MessagePart { return c.parts }

// Merge two component types together. Used when we have definitions for one overall FIX version.
func (c ComponentType) Merge(coth *ComponentType) error {
	fieldByTag := make(map[int]*FieldDef)
	for _, fld := range c.fields {
		fieldByTag[fld.Tag()] = fld
	}

	for _, fld := range coth.fields {
		fldtag := fld.Tag()
		if _, exists := fieldByTag[fldtag]; !exists {
			fieldByTag[fldtag] = fld
			c.fields = append(c.fields, fld)

			if fld.Required() {
				c.requiredFields = append(c.requiredFields, fld)
			}
		} else {
			fieldByTag[fldtag].Merge(fld)
		}
	}

	return nil
}

// TagSet is set for tags.
type TagSet map[int]struct{}

// Add adds a tag to the tagset.
func (t TagSet) Add(tag int) {
	t[tag] = struct{}{}
}

// Component is a Component as it appears in a given MessageDef.
type Component struct {
	*ComponentType
	required bool
}

// NewComponent returns an initialized Component instance.
func NewComponent(ct *ComponentType, required bool) *Component {
	return &Component{
		ComponentType: ct,
		required:      required,
	}
}

// Required returns true if this component is required for the containing
// MessageDef.
func (c Component) Required() bool { return c.required }

// Field models a field or repeating group in a message.
type Field interface {
	Tag() int
}

// FieldDef models a field belonging to a message.
type FieldDef struct {
	*FieldType
	required bool

	Parts          []MessagePart
	Fields         []*FieldDef
	requiredParts  []MessagePart
	requiredFields []*FieldDef
}

// NewFieldDef returns an initialized FieldDef.
func NewFieldDef(fieldType *FieldType, required bool) *FieldDef {
	return &FieldDef{
		FieldType: fieldType,
		required:  required,
	}
}

// NewGroupFieldDef returns an initialized FieldDef for a repeating group.
func NewGroupFieldDef(fieldType *FieldType, required bool, parts []MessagePart) *FieldDef {
	field := FieldDef{
		FieldType: fieldType,
		required:  required,
		Parts:     parts,
	}

	for _, part := range parts {
		if part.Required() {
			field.requiredParts = append(field.requiredParts, part)
		}

		if comp, ok := part.(Component); ok {
			field.Fields = append(field.Fields, comp.Fields()...)

			if comp.required {
				field.requiredFields = append(field.requiredFields, comp.requiredFields...)
			}
		} else {
			if child, ok := part.(*FieldDef); ok {
				field.Fields = append(field.Fields, child)

				if child.required {
					field.requiredFields = append(field.requiredFields, child)
				}
			} else {
				panic("unknown part")
			}
		}
	}

	return &field
}

// Required returns true if this FieldDef is required for the containing
// MessageDef.
func (f FieldDef) Required() bool { return f.required }

// IsGroup is true if the field is a repeating group.
func (f FieldDef) IsGroup() bool {
	return len(f.Fields) > 0
}

// RequiredParts returns those parts that are required for this FieldDef. IsGroup
// must return true.
func (f FieldDef) RequiredParts() []MessagePart { return f.requiredParts }

// RequiredFields returns those fields that are required for this FieldDef. IsGroup
// must return true.
func (f FieldDef) RequiredFields() []*FieldDef { return f.requiredFields }

func (f FieldDef) childTags() []int {
	tags := make([]int, 0, len(f.Fields))

	for _, f := range f.Fields {
		tags = append(tags, f.Tag())
		tags = append(tags, f.childTags()...)
	}

	return tags
}

func (f *FieldDef) Merge(oth *FieldDef) error {
	if f.tag != oth.tag {
		return nil
	}

	if oth.Enums != nil {
		for ek, ev := range oth.Enums {
			if f.Enums == nil {
				f.Enums = oth.Enums
				continue
			}

			if _, exists := f.Enums[ek]; !exists {
				f.Enums[ek] = ev
			}
		}
	}

	if f.IsGroup() && oth.IsGroup() {
		existingGrpTags := make(map[int]*FieldDef)
		for _, ffld := range f.Fields {
			existingGrpTags[ffld.tag] = ffld
		}

		for _, ogfld := range oth.Fields {
			if exfld, exists := existingGrpTags[ogfld.tag]; exists {
				exfld.Merge(ogfld)
			} else {
				f.Fields = append(f.Fields, ogfld)
			}
		}
	}

	return nil
}

// FieldType holds information relating to a field.  Includes Tag, type, and enums, if defined.
type FieldType struct {
	name  string
	tag   int
	Type  string
	Enums map[string]Enum
}

// NewFieldType returns a pointer to an initialized FieldType.
func NewFieldType(name string, tag int, fixType string) *FieldType {
	return &FieldType{
		name: name,
		tag:  tag,
		Type: fixType,
	}
}

// Name returns the name for this FieldType.
func (f FieldType) Name() string { return f.name }

// Tag returns the tag for this fieldType.
func (f FieldType) Tag() int { return f.tag }

func (f *FieldType) Merge(oth *FieldType) error {
	if f.tag != oth.tag {
		return nil
	}

	if f.Enums != nil {
		for ek, ev := range oth.Enums {
			if _, exists := f.Enums[ek]; !exists {
				f.Enums[ek] = ev
			}
		}
	} else {
		f.Enums = oth.Enums
	}

	return nil
}

// Enum is a container for value and description.
type Enum struct {
	Value       string
	Description string
}

// MessageDef can apply to header, trailer, or body of a FIX Message.
type MessageDef struct {
	Name    string
	MsgType string
	Fields  map[int]*FieldDef
	// Parts are the MessageParts of contained in this MessageDef in declaration order.
	Parts         []MessagePart
	requiredParts []MessagePart

	RequiredTags TagSet
	Tags         TagSet
}

func (m MessageDef) UniqueFields() *FieldDef {
	return nil
}

// RequiredParts returns those parts that are required for this Message.
func (m MessageDef) RequiredParts() []MessagePart { return m.requiredParts }

// Merge another MessageDef into this MessageDef
func (m *MessageDef) Merge(other *MessageDef) error {

	for tag, fld := range other.Fields {
		if existingFld, exists := m.Fields[tag]; !exists {
			fmt.Printf("Trying to add NEW message field (%s) %s\n", m.Name, fld.name)
			m.Fields[tag] = fld
			m.Tags.Add(tag)
		} else {
			fmt.Printf("Trying to merge existing message field (%s) %s\n", m.Name, fld.name)
			existingFld.Merge(fld)
		}

		if fld.IsGroup() {
			for _, subfld := range fld.Fields {
				subtag := subfld.Tag()
				if existingFld, exists := m.Fields[subtag]; !exists {
					fmt.Printf("Trying to add NEW GROUP message field (%s) %s\n", m.Name, subfld.name)
					m.Fields[subtag] = subfld
					m.Tags.Add(subtag)
				} else {
					fmt.Printf("Trying to merge existing GROUP message field (%s) %s\n", m.Name, subfld.name)
					existingFld.Merge(subfld)
				}
			}
		}
	}

	partsByName := make(map[string]MessagePart)
	for _, mpart := range m.Parts {
		partsByName[mpart.Name()] = mpart
	}

	for _, opart := range other.Parts {
		if existingPart, exists := partsByName[opart.Name()]; exists {
			switch epType := existingPart.(type) {
			case messagePartWithFields:

				fmt.Printf("Trying to merge existing message type (%s) part %s\n", m.Name, epType.Name())
				opType := opart.(messagePartWithFields)
				for _, fld := range opType.Fields() {
					if existingFld, exists := m.Fields[fld.tag]; !exists {
						fmt.Printf("Trying to add part field (%s) %s\n", m.Name, fld.name)
						m.Fields[fld.tag] = fld
						m.Tags.Add(fld.tag)
					} else {
						fmt.Printf("Trying to merge part field (%s) %s\n", m.Name, fld.name)
						existingFld.Merge(fld)
					}
				}

			case *FieldDef:
				opType := opart.(*FieldDef)
				fmt.Printf("Trying to merge existing message type (%s) field %s\n", m.Name, epType.Name())
				if existingFld, exists := m.Fields[opType.tag]; !exists {
					fmt.Printf("Trying to add message field (%s) %s\n", m.Name, opType.name)
					m.Fields[opType.tag] = opType
					m.Tags.Add(opType.tag)
				} else {
					fmt.Printf("Trying to merge message field (%s) %s\n", m.Name, opType.name)
					existingFld.Merge(opType)
				}

			default:
				continue
			}
		} else {
			m.Parts = append(m.Parts, opart)
			switch opType := opart.(type) {
			case messagePartWithFields:
				for _, fld := range opType.Fields() {
					if existingFld, exists := m.Fields[fld.tag]; !exists {
						fmt.Printf("Trying to add NEW part field (%s) %s\n", m.Name, opType.Name())
						m.Fields[fld.tag] = fld
						m.Tags.Add(fld.tag)
					} else {
						fmt.Printf("Trying to merge NEW part field (%s) %s\n", m.Name, opType.Name())
						existingFld.Merge(fld)
					}
				}

			case *FieldDef:
				if existingFld, exists := m.Fields[opType.tag]; !exists {
					fmt.Printf("Trying to merge NEW message field (%s) %s\n", m.Name, opType.name)
					m.Fields[opType.tag] = opType
					m.Tags.Add(opType.tag)
				} else {
					fmt.Printf("Trying to merge message field (%s) %s\n", m.Name, opType.name)
					existingFld.Merge(opType)
				}

			default:
				continue
			}
		}
	}

	for _, fld := range m.Fields {
		if fld.IsGroup() {
			for _, subfld := range fld.Fields {
				subtag := subfld.Tag()
				if existingFld, exists := m.Fields[subtag]; !exists {
					m.Fields[subtag] = subfld
					m.Tags.Add(subtag)
				} else {
					existingFld.Merge(subfld)
				}
			}
		}
	}

	for _, fld := range m.Fields {
		fmt.Printf("Message (%s) [%p] contains fld %s [%d]\n", m.Name, m, fld.name, fld.tag)
	}

	return nil
}

// NewMessageDef returns a pointer to an initialized MessageDef.
func NewMessageDef(name, msgType string, parts []MessagePart) *MessageDef {
	msg := MessageDef{
		Name:         name,
		MsgType:      msgType,
		Fields:       make(map[int]*FieldDef),
		RequiredTags: make(TagSet),
		Tags:         make(TagSet),
		Parts:        parts,
	}

	processField := func(field *FieldDef, allowRequired bool) {
		msg.Fields[field.Tag()] = field
		msg.Tags.Add(field.Tag())
		for _, t := range field.childTags() {
			msg.Tags.Add(t)
		}

		if allowRequired && field.Required() {
			msg.RequiredTags.Add(field.Tag())
		}
	}

	for _, part := range parts {
		if part.Required() {
			msg.requiredParts = append(msg.requiredParts, part)
		}

		switch pType := part.(type) {
		case messagePartWithFields:
			for _, f := range pType.Fields() {
				// Field if required in component is required in message only if
				// component is required.
				processField(f, pType.Required())
			}

		case *FieldDef:
			processField(pType, true)

		default:
			panic("Unknown Part")
		}
	}

	return &msg
}

// Parse loads and build a datadictionary instance from an xml file.
func Parse(path string) (*DataDictionary, error) {
	var xmlFile *os.File
	var err error
	xmlFile, err = os.Open(path)
	if err != nil {
		return nil, errors.Wrapf(err, "problem opening file: %v", path)
	}
	defer xmlFile.Close()

	return ParseSrc(xmlFile)
}

// ParseSrc loads and build a datadictionary instance from an xml source.
func ParseSrc(xmlSrc io.Reader) (*DataDictionary, error) {
	doc := new(XMLDoc)
	decoder := xml.NewDecoder(xmlSrc)
	if err := decoder.Decode(doc); err != nil {
		return nil, errors.Wrapf(err, "problem parsing XML file")
	}

	b := new(builder)
	dict, err := b.build(doc)
	if err != nil {
		return nil, err
	}

	return dict, nil
}

func (ours *DataDictionary) Merge(other *DataDictionary) error {

	newFieldDefs := make([]*FieldDef, 0)

	for mk, mv := range other.Messages {
		if ourVal, exists := ours.Messages[mk]; exists {
			fmt.Printf("Merging message %s from %s %d.%d [%p -> %p] (%p -> %p)\n", mk, other.FIXType, other.Major, other.Minor, other, ours, mv, ourVal)
			ourVal.Merge(mv)
		} else {
			fmt.Printf("Adding missing message %s from %s %d.%d [%p -> %p]\n", mk, other.FIXType, other.Major, other.Minor, other, ours)
			ours.Messages[mk] = mv
		}

		for ftag, fld := range mv.Fields {
			fmt.Printf("Evaluating message %s (%p / %p) field %s [Tag %d]\n", mv.Name, mv, ours, fld.name, ftag)
			if _, exists := ours.FieldTypeByTag[ftag]; !exists {
				fmt.Printf("Adding message %s field %s [Tag %d]\n", mv.Name, fld.name, ftag)
				ours.FieldTypeByTag[ftag] = fld.FieldType
				ours.FieldTypeByName[fld.name] = fld.FieldType
				newFieldDefs = append(newFieldDefs, fld)
			}
		}

		for _, part := range mv.Parts {
			switch epType := part.(type) {
			case messagePartWithFields:

				for _, fld := range epType.Fields() {
					if _, exists := ours.FieldTypeByTag[fld.tag]; !exists {
						ours.FieldTypeByTag[fld.tag] = fld.FieldType
						ours.FieldTypeByName[fld.name] = fld.FieldType
						newFieldDefs = append(newFieldDefs, fld)
					}
				}

			case *FieldDef:
				if _, exists := ours.FieldTypeByTag[epType.tag]; !exists {
					ours.FieldTypeByTag[epType.tag] = epType.FieldType
					ours.FieldTypeByName[epType.name] = epType.FieldType
					newFieldDefs = append(newFieldDefs, epType)
				}

			default:
				continue
			}
		}
	}

	for ck, kv := range other.ComponentTypes {
		if ourVal, exists := ours.ComponentTypes[ck]; exists {
			ourVal.Merge(kv)
		} else {
			ours.ComponentTypes[ck] = kv
		}

		for ftag, fld := range kv.Fields() {
			fmt.Printf("Evaluating component %s field %s [Tag %d]\n", kv.Name(), fld.name, ftag)
			if _, exists := ours.FieldTypeByTag[ftag]; !exists {
				fmt.Printf("Adding component %s field %s [Tag %d]\n", kv.Name(), fld.name, ftag)
				ours.FieldTypeByTag[ftag] = fld.FieldType
				ours.FieldTypeByName[fld.name] = fld.FieldType
				newFieldDefs = append(newFieldDefs, fld)

			}
		}

		for _, part := range kv.Parts() {
			switch epType := part.(type) {
			case messagePartWithFields:

				for _, fld := range epType.Fields() {
					if _, exists := ours.FieldTypeByTag[fld.tag]; !exists {
						ours.FieldTypeByTag[fld.tag] = fld.FieldType
						ours.FieldTypeByName[fld.name] = fld.FieldType
						newFieldDefs = append(newFieldDefs, fld)
					}
				}

			case *FieldDef:
				if _, exists := ours.FieldTypeByTag[epType.tag]; !exists {
					ours.FieldTypeByTag[epType.tag] = epType.FieldType
					ours.FieldTypeByName[epType.name] = epType.FieldType
					newFieldDefs = append(newFieldDefs, epType)
				}

			default:
				continue
			}
		}
	}

	for _, msgd := range ours.Messages {
		for tag, fld := range msgd.Fields {
			if existingFld, exists := ours.FieldTypeByTag[tag]; !exists {
				fmt.Printf("Ugh message %s new field def %s [Tag %d]\n", msgd.Name, fld.name, fld.tag)
				ours.FieldTypeByTag[tag] = fld.FieldType
				ours.FieldTypeByName[fld.name] = fld.FieldType
			} else {
				fmt.Printf("Maybe message %s [%p] field def %s [Tag %d|%d] exists in %p\n", msgd.Name, msgd, fld.name, fld.tag, tag, ours)
				if existingFld.name != fld.name {
					fmt.Printf("TAG id conflict!!! %d %s vs %s\n", tag, existingFld.name, fld.name)
					ours.FieldTypeByName[fld.name] = fld.FieldType
				}
			}

		}
	}

	for _, fld := range newFieldDefs {
		fmt.Printf("Migrating new field def %s [Tag %d]\n", fld.name, fld.tag)
		if fld.IsGroup() {
			for fsubtag, subfld := range fld.Fields {
				if _, exists := ours.FieldTypeByTag[fsubtag]; !exists {
					ours.FieldTypeByTag[fsubtag] = subfld.FieldType
					ours.FieldTypeByName[subfld.name] = subfld.FieldType
				}
			}
		}
	}

	for tag, othFld := range other.FieldTypeByTag {
		_, exists := ours.FieldTypeByTag[tag]
		if exists {
			ours.FieldTypeByTag[tag].Merge(othFld)
		} else {
			ours.FieldTypeByTag[tag] = othFld
			ours.FieldTypeByName[othFld.Name()] = othFld
		}
	}

	for name, othFld := range other.FieldTypeByName {
		if _, exists := ours.FieldTypeByName[name]; !exists {
			ours.FieldTypeByTag[othFld.tag] = othFld
			ours.FieldTypeByName[name] = othFld
		}
	}

	return nil
}
