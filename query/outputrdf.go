/*
 * Copyright 2017-2020 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package query

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/dgraph-io/dgraph/algo"
	"github.com/dgraph-io/dgraph/protos/pb"
	"github.com/dgraph-io/dgraph/types"
	"github.com/dgraph-io/dgraph/x"
	"github.com/pkg/errors"
)

// rdfBuilder is used to generate RDF from subgraph.
type rdfBuilder struct {
	buf *bytes.Buffer
}

// ToRDF converts the given subgraph list into rdf format.
func ToRDF(l *Latency, sgl []*SubGraph) ([]byte, error) {
	b := &rdfBuilder{
		buf: &bytes.Buffer{},
	}
	for _, sg := range sgl {
		if err := validateSubGraphForRDF(sg); err != nil {
			return nil, err
		}
		// Skip parent graph. we don't want parent values.
		for _, child := range sg.Children {
			if err := b.castToRDF(child); err != nil {
				return nil, err
			}
		}
	}
	return b.buf.Bytes(), nil
}

// castToRDF converts the given subgraph to RDF and appends to the
// output string.
func (b *rdfBuilder) castToRDF(sg *SubGraph) error {
	if err := validateSubGraphForRDF(sg); err != nil {
		return err
	}
	if sg.SrcUIDs != nil {
		// Get RDF for the given subgraph.
		if err := b.rdfForSubgraph(sg); err != nil {
			return err
		}
	}
	// Recursively cnvert RDF for the children graph.
	for _, child := range sg.Children {
		if err := b.castToRDF(child); err != nil {
			return err
		}
	}
	return nil
}

// rdfForSubgraph generates RDF and appends to the output parameter.
func (b *rdfBuilder) rdfForSubgraph(sg *SubGraph) error {
	for i, uid := range sg.SrcUIDs.Uids {
		if sg.Params.IgnoreResult {
			// Skip ignored values.
			continue
		}
		if sg.IsInternal() {
			if sg.Params.Expand != "" {
				continue
			}
			// Check if we have val for the given uid. If you got uid then populate
			// the rdf.
			val, ok := sg.Params.UidToVal[uid]
			if !ok && val.Value == nil {
				continue
			}
			outputval, err := valToBytes(val)
			if err != nil {
				return err
			}
			b.writeRDF(uid, []byte(sg.aggWithVarFieldName()), outputval)
			continue
		}
		switch {
		case len(sg.counts) > 0:
			// Add count rdf.
			b.rdfForCount(uid, sg.counts[i], sg)
		case i < len(sg.uidMatrix) && len(sg.uidMatrix[i].Uids) != 0:
			// Add posting list relation.
			b.rdfForUIDList(uid, sg.uidMatrix[i], sg)
		case i < len(sg.valueMatrix):
			b.rdfForValueList(uid, sg.valueMatrix[i], sg.fieldName())
		}
	}
	return nil
}

func (b *rdfBuilder) writeRDF(subject uint64, predicate []byte, object []byte) {
	// add subject
	b.writeTriple([]byte(fmt.Sprintf("%#x", subject)))
	x.Check(b.buf.WriteByte(' '))
	// add predicate
	b.writeTriple(predicate)
	x.Check(b.buf.WriteByte(' '))
	// add object
	x.Check2(b.buf.Write(object))
	x.Check(b.buf.WriteByte(' '))
	x.Check(b.buf.WriteByte('.'))
	x.Check(b.buf.WriteByte('\n'))
}

func (b *rdfBuilder) writeTriple(val []byte) {
	x.Check(b.buf.WriteByte('<'))
	x.Check2(b.buf.Write(val))
	x.Check(b.buf.WriteByte('>'))
}

// rdfForCount returns rdf for count fucntion.
func (b *rdfBuilder) rdfForCount(subject uint64, count uint32, sg *SubGraph) {
	fieldName := sg.Params.Alias
	if fieldName == "" {
		fieldName = fmt.Sprintf("count(%s)", sg.Attr)
	}
	b.writeRDF(subject, []byte(fieldName), []byte(strconv.FormatUint(uint64(count), 10)))
}

// rdfForUIDList returns rdf for uid list.
func (b *rdfBuilder) rdfForUIDList(subject uint64, list *pb.List, sg *SubGraph) {
	for _, destUID := range list.Uids {
		if algo.IndexOf(sg.DestUIDs, destUID) < 0 {
			// This uid is filtered.
			continue
		}
		// Build object.
		b.writeRDF(
			subject,
			[]byte(sg.fieldName()),
			buildTriple([]byte(fmt.Sprintf("%#x", destUID))))
	}
}

// rdfForValueList returns rdf for the value list.
func (b *rdfBuilder) rdfForValueList(subject uint64, valueList *pb.ValueList, attr string) {
	if attr == "uid" {
		b.writeRDF(subject,
			[]byte(attr),
			buildTriple([]byte(fmt.Sprintf("%#x", subject))))
		return
	}
	for _, destValue := range valueList.Values {
		val, err := convertWithBestEffort(destValue, attr)
		if err != nil {
			continue
		}
		outputval, err := valToBytes(val)
		if err != nil {
			continue
		}
		switch val.Tid {
		case types.UidID:
			b.writeRDF(subject, []byte(attr), buildTriple(outputval))
		default:
			b.writeRDF(subject, []byte(attr), outputval)
		}
	}
}

func buildTriple(val []byte) []byte {
	buf := make([]byte, 0, 2+len(val))
	buf = append(buf, '<')
	buf = append(buf, val...)
	buf = append(buf, '>')
	return buf
}

func validateSubGraphForRDF(sg *SubGraph) error {
	if sg.IsGroupBy() {
		return errors.New("groupby is not supported in rdf output format")
	}
	uidCount := sg.Attr == "uid" && sg.Params.DoCount && sg.IsInternal()
	if uidCount {
		return errors.New("uid count is not supported in the rdf output format")
	}
	if sg.Params.Normalize {
		return errors.New("normalize directive is not supported in the rdf output format")
	}
	if sg.Params.IgnoreReflex {
		return errors.New("ignorereflex directive is not supported in the rdf output format")
	}
	if sg.SrcFunc != nil && sg.SrcFunc.Name == "checkpwd" {
		return errors.New("chkpwd function is not supported in the rdf output format")
	}
	if len(sg.facetsMatrix) != 0 {
		return errors.New("facet is not supported in the rdf output format")
	}
	return nil
}
