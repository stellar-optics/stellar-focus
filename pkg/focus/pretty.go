// Package focus provides XDR-aware assertions for Go tests.
package focus

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/stellar-optics/stellar-xdr-lens/pkg/lens"
)

// Pretty formats an XDR value as a concise, human-readable string.
//
// It accepts the same input forms as the assertions: a base64 string, raw
// []byte, any xdr.* type implementing encoding.BinaryMarshaler, or a
// *lens.Value. The output is deterministic and intended for debugging and
// test failure messages, not for parsing.
//
// The formatter uses lens to decode the value, then walks the neutral Node
// tree producing a compact representation that shows field names and enriched
// values (e.g., stroops shown with XLM equivalents, addresses shown as
// strkeys). It is the same vocabulary that appears in focus assertion
// failures, so a pretty-printed value and a failing diff use the same field
// paths and value renderings.
//
// Example output for a payment envelope:
//
//	TransactionEnvelope {
//	  V1.Tx.SourceAccount: GCJNHFXJEQEY54NPKU4DBMT5WOBJUHMYSFBDAQF4OMTEGJN4BQU7ZST6
//	  V1.Tx.Fee: 100 (0.0000100 XLM)
//	  V1.Tx.SeqNum: 12345
//	  V1.Tx.Operations[0].Body.PaymentOp.Destination: GAPB7YMUQ6MPQ23PTFDNBZP7HGGTZPI2DT5JJURUOIXNPLGHW2O3WYXZ
//	  V1.Tx.Operations[0].Body.PaymentOp.Asset: native
//	  V1.Tx.Operations[0].Body.PaymentOp.Amount: 1000000000 (100.0000000 XLM)
//	  V1.Tx.Memo: none
//	}
func Pretty(v Value) string {
	decoded, err := coerce(v, "")
	if err != nil {
		return fmt.Sprintf("(invalid: %v)", err)
	}
	return formatNode(decoded.Node, 0, true, decoded.Type)
}

// PrettyAs formats an XDR value decoded as the named type.
//
// This is preferred when the type is known, because type detection is
// ambiguous for short payloads. The typeName should be one of the types
// listed by `lens types` (e.g., "TransactionEnvelope", "TransactionResult",
// "TransactionMeta", "LedgerEntry", "Operation").
func PrettyAs(typeName string, v Value) string {
	decoded, err := coerce(v, typeName)
	if err != nil {
		return fmt.Sprintf("(invalid: %v)", err)
	}
	return formatNode(decoded.Node, 0, true, decoded.Type)
}

// formatNode recursively formats a lens Node tree.
func formatNode(n *lens.Node, indent int, isRoot bool, rootType string) string {
	if n == nil {
		return "(nil)"
	}

	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	// For root node, also apply known struct formatting
	if isRoot {
		if enriched := formatKnownStruct(n, indent, rootType); enriched != "" {
			return enriched
		}
		if n.TypeName != "" {
			fmt.Fprintf(&b, "%s%s {\n", prefix, n.TypeName)
			formatChildren(&b, n.Children, indent+1, rootType)
			b.WriteString(prefix + "}")
			return b.String()
		}
	}

	// For non-root nodes, format based on kind
	switch n.Kind {
	case lens.KindStruct:
		// Special handling for known struct types
		if enriched := formatKnownStruct(n, indent, rootType); enriched != "" {
			return enriched
		}
		if n.Name != "" {
			fmt.Fprintf(&b, "%s%s {\n", prefix, n.Name)
		} else {
			fmt.Fprintf(&b, "%s{\n", prefix)
		}
		formatChildren(&b, n.Children, indent+1, rootType)
		b.WriteString(prefix + "}")
	case lens.KindList:
		if n.Name != "" {
			fmt.Fprintf(&b, "%s%s [\n", prefix, n.Name)
		} else {
			fmt.Fprintf(&b, "%s[\n", prefix)
		}
		for i, child := range n.Children {
			childPrefix := strings.Repeat("  ", indent+1)
			if child.Kind == lens.KindStruct {
				childStr := formatNode(child, indent+1, false, rootType)
				lines := strings.Split(strings.TrimSpace(childStr), "\n")
				fmt.Fprintf(&b, "%s[%d] %s\n", childPrefix, i, lines[0])
				for _, line := range lines[1:] {
					fmt.Fprintf(&b, "%s  %s\n", childPrefix, line)
				}
			} else {
				fmt.Fprintf(&b, "%s[%d]: %s\n", childPrefix, i, formatNode(child, 0, false, rootType))
			}
		}
		b.WriteString(prefix + "]")
	case lens.KindScalar:
		val := formatScalar(n.Name, n.Value)
		if n.Name != "" {
			fmt.Fprintf(&b, "%s%s: %s", prefix, n.Name, val)
		} else {
			fmt.Fprintf(&b, "%s%s", prefix, val)
		}
	}
	return b.String()
}

// formatKnownStruct provides enriched formatting for known XDR struct types.
func formatKnownStruct(n *lens.Node, indent int, rootType string) string {
	if n == nil {
		return ""
	}

	// Check for Memo type
	if n.TypeName == "Memo" || (n.Name == "Memo" && n.TypeName == "") {
		return formatMemo(n, indent)
	}

	// Check for ScVal type (root or nested)
	if n.TypeName == "ScVal" || (rootType == "ScVal" && n.Name == "") {
		return formatScValNode(n, indent)
	}

	// Check for Asset type (struct or scalar)
	if n.TypeName == "Asset" {
		return formatAssetNode(n, indent)
	}

	// For non-struct types, only handle specific known types
	if n.Kind != lens.KindStruct {
		// Check for LiquidityPoolType scalar
		if n.TypeName == "LiquidityPoolType" {
			return formatLiquidityPoolTypeNode(n, indent)
		}
		return ""
	}

	// Check for OperationBody type
	if n.TypeName == "OperationBody" {
		return formatOperationBodyNode(n, indent)
	}

	// Check for PaymentOp, CreateAccountOp, etc.
	if strings.HasSuffix(n.TypeName, "Op") && n.TypeName != "Operation" {
		return formatOperationOpNode(n, indent)
	}

	// Check for Result types (PaymentResult, etc.)
	if strings.HasSuffix(n.TypeName, "Result") && n.TypeName != "Result" && n.TypeName != "TransactionResult" {
		return formatResultNode(n, indent)
	}

	return ""
}

// formatOperationOpNode formats an operation-specific struct (PaymentOp, etc.)
func formatOperationOpNode(n *lens.Node, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	if n.Name != "" {
		fmt.Fprintf(&b, "%s%s {\n", prefix, n.Name)
	} else {
		fmt.Fprintf(&b, "%s{\n", prefix)
	}
	formatChildren(&b, n.Children, indent+1, "")
	b.WriteString(prefix + "}")
	return b.String()
}

// formatResultNode formats an operation result struct
func formatResultNode(n *lens.Node, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	if n.Name != "" {
		fmt.Fprintf(&b, "%s%s {\n", prefix, n.Name)
	} else {
		fmt.Fprintf(&b, "%s{\n", prefix)
	}
	formatChildren(&b, n.Children, indent+1, "")
	b.WriteString(prefix + "}")
	return b.String()
}

// formatLiquidityPoolTypeNode formats a LiquidityPoolType with short name
func formatLiquidityPoolTypeNode(n *lens.Node, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	poolType := ""
	if n.Value != nil {
		poolType = fmt.Sprintf("%v", n.Value)
	}

	// Convert long type names to short ones
	shortType := poolType
	if poolType == "LiquidityPoolTypeLiquidityPoolConstantProduct" {
		shortType = "constantProduct"
	}

	if n.Name != "" {
		fmt.Fprintf(&b, "%s%s: %s", prefix, n.Name, shortType)
	} else {
		fmt.Fprintf(&b, "%s%s", prefix, shortType)
	}
	return b.String()
}

// formatMemo formats a Memo struct with enriched output.
func formatMemo(n *lens.Node, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	memoType := ""
	var textVal string
	var idVal uint64
	var hashVal string
	var retHashVal string

	for _, child := range n.Children {
		switch child.Name {
		case "Type":
			if child.Value != nil {
				memoType = fmt.Sprintf("%v", child.Value)
			}
		case "Text":
			if child.Value != nil {
				textVal = fmt.Sprintf("%v", child.Value)
			}
		case "Id":
			if child.Value != nil {
				if v, ok := child.Value.(uint64); ok {
					idVal = v
				}
			}
		case "Hash":
			if child.Value != nil {
				hashVal = fmt.Sprintf("%v", child.Value)
			}
		case "RetHash":
			if child.Value != nil {
				retHashVal = fmt.Sprintf("%v", child.Value)
			}
		}
	}

	var memoStr string
	switch memoType {
	case "MemoTypeMemoNone":
		memoStr = "none"
	case "MemoTypeMemoText":
		memoStr = fmt.Sprintf("text:%s", textVal)
	case "MemoTypeMemoId":
		memoStr = fmt.Sprintf("id:%d", idVal)
	case "MemoTypeMemoHash":
		memoStr = fmt.Sprintf("hash:%s", hashVal)
	case "MemoTypeMemoReturn":
		memoStr = fmt.Sprintf("return:%s", retHashVal)
	default:
		memoStr = memoType
	}

	if n.Name != "" {
		fmt.Fprintf(&b, "%s%s: %s", prefix, n.Name, memoStr)
	} else {
		fmt.Fprintf(&b, "%s%s", prefix, memoStr)
	}
	return b.String()
}

// formatScValNode formats an ScVal with enriched output.
func formatScValNode(n *lens.Node, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	scValType := ""
	var val any
	var addressAccountID string

	for _, child := range n.Children {
		switch child.Name {
		case "Type":
			if child.Value != nil {
				scValType = fmt.Sprintf("%v", child.Value)
			}
		case "Address":
			// Address is a struct with Type and AccountId/ContractId
			if child.Kind == lens.KindStruct {
				for _, gc := range child.Children {
					if gc.Name == "AccountId" && gc.Value != nil {
						addressAccountID = fmt.Sprintf("%v", gc.Value)
					}
				}
			}
		default:
			// The value field - could be B, U32, I32, U64, I64, Str, Sym, Bytes, Vec, Map, Address, etc.
			if !child.IsZero() && child.Kind == lens.KindScalar {
				val = child.Value
			}
		}
	}

	var formatted string
	switch scValType {
	case "ScValTypeScvVoid":
		formatted = "void"
	case "ScValTypeScvBool":
		if val != nil {
			if v, ok := val.(bool); ok {
				formatted = fmt.Sprintf("bool(%v)", v)
			} else {
				formatted = fmt.Sprintf("bool(%v)", val)
			}
		} else {
			formatted = "bool"
		}
	case "ScValTypeScvU32":
		if val != nil {
			formatted = fmt.Sprintf("u32(%v)", val)
		} else {
			formatted = "u32"
		}
	case "ScValTypeScvI32":
		if val != nil {
			formatted = fmt.Sprintf("i32(%v)", val)
		} else {
			formatted = "i32"
		}
	case "ScValTypeScvU64":
		if val != nil {
			formatted = fmt.Sprintf("u64(%v)", val)
		} else {
			formatted = "u64"
		}
	case "ScValTypeScvI64":
		if val != nil {
			formatted = fmt.Sprintf("i64(%v)", val)
		} else {
			formatted = "i64"
		}
	case "ScValTypeScvSymbol":
		if val != nil {
			formatted = fmt.Sprintf("sym(%v)", val)
		} else {
			formatted = "sym"
		}
	case "ScValTypeScvString":
		if val != nil {
			formatted = fmt.Sprintf("str(%q)", val)
		} else {
			formatted = "str"
		}
	case "ScValTypeScvBytes":
		if val != nil {
			formatted = fmt.Sprintf("bytes(%v)", val)
		} else {
			formatted = "bytes"
		}
	case "ScValTypeScvVec":
		if val != nil {
			formatted = fmt.Sprintf("vec[%v]", val)
		} else {
			formatted = "vec"
		}
	case "ScValTypeScvMap":
		if val != nil {
			formatted = fmt.Sprintf("map[%v]", val)
		} else {
			formatted = "map"
		}
	case "ScValTypeScvAddress":
		switch {
		case addressAccountID != "":
			formatted = fmt.Sprintf("addr(%s)", addressAccountID)
		case val != nil:
			formatted = fmt.Sprintf("addr(%v)", val)
		default:
			formatted = "addr"
		}
	default:
		formatted = scValType
	}

	if n.Name != "" {
		fmt.Fprintf(&b, "%s%s: %s", prefix, n.Name, formatted)
	} else {
		fmt.Fprintf(&b, "%s%s", prefix, formatted)
	}
	return b.String()
}

// formatAssetNode formats an Asset with enriched output.
func formatAssetNode(n *lens.Node, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	// Handle scalar Asset (lens returns enriched value directly)
	if n.Kind == lens.KindScalar && n.Value != nil {
		formatted := fmt.Sprintf("%v", n.Value)
		if n.Name != "" {
			fmt.Fprintf(&b, "%s%s: %s", prefix, n.Name, formatted)
		} else {
			fmt.Fprintf(&b, "%s%s", prefix, formatted)
		}
		return b.String()
	}

	assetType := ""
	var code string
	var issuer string

	for _, child := range n.Children {
		switch child.Name {
		case "Type":
			if child.Value != nil {
				assetType = fmt.Sprintf("%v", child.Value)
			}
		case "AlphaNum4":
			if child.Kind == lens.KindStruct {
				for _, gc := range child.Children {
					if gc.Name == "AssetCode" && gc.Value != nil {
						code = fmt.Sprintf("%v", gc.Value)
					}
					if gc.Name == "Issuer" && gc.Value != nil {
						issuer = fmt.Sprintf("%v", gc.Value)
					}
				}
			}
		case "AlphaNum12":
			if child.Kind == lens.KindStruct {
				for _, gc := range child.Children {
					if gc.Name == "AssetCode" && gc.Value != nil {
						code = fmt.Sprintf("%v", gc.Value)
					}
					if gc.Name == "Issuer" && gc.Value != nil {
						issuer = fmt.Sprintf("%v", gc.Value)
					}
				}
			}
		}
	}

	var formatted string
	switch assetType {
	case "AssetTypeAssetTypeNative":
		formatted = "native"
	case "AssetTypeAssetTypeCreditAlphanum4", "AssetTypeAssetTypeCreditAlphanum12":
		switch {
		case code != "" && issuer != "":
			formatted = fmt.Sprintf("%s:%s", code, issuer)
		case code != "":
			formatted = code
		default:
			formatted = assetType
		}
	default:
		formatted = assetType
	}

	if n.Name != "" {
		fmt.Fprintf(&b, "%s%s: %s", prefix, n.Name, formatted)
	} else {
		fmt.Fprintf(&b, "%s%s", prefix, formatted)
	}
	return b.String()
}

// formatOperationBodyNode formats an OperationBody with enriched output.
func formatOperationBodyNode(n *lens.Node, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	opType := ""
	for _, child := range n.Children {
		if child.Name == "Type" && child.Value != nil {
			opType = fmt.Sprintf("%v", child.Value)
			break
		}
	}

	// Format operation type as short code (e.g., "payment" instead of "OperationTypePayment")
	shortOpType := formatOperationType(opType)

	// For OperationBody, show the type and then the specific operation
	// Find the operation arm (PaymentOp, CreateAccountOp, etc.)
	for _, child := range n.Children {
		if child.Name != "Type" && !child.IsZero() {
			// This is the operation arm, format it
			childStr := formatNode(child, indent, false, "")
			if n.Name != "" {
				fmt.Fprintf(&b, "%s%s: %s {\n", prefix, n.Name, shortOpType)
			} else {
				fmt.Fprintf(&b, "%s%s {\n", prefix, shortOpType)
			}
			lines := strings.Split(strings.TrimSpace(childStr), "\n")
			for _, line := range lines {
				b.WriteString("  " + line + "\n")
			}
			b.WriteString(prefix + "}")
			return b.String()
		}
	}

	// Fallback
	if n.Name != "" {
		fmt.Fprintf(&b, "%s%s: %s", prefix, n.Name, shortOpType)
	} else {
		fmt.Fprintf(&b, "%s%s", prefix, shortOpType)
	}
	return b.String()
}

// formatOperationType converts long operation type names to short codes.
func formatOperationType(opType string) string {
	if strings.HasPrefix(opType, "OperationType") {
		short := strings.TrimPrefix(opType, "OperationType")
		return toSnakeCase(short)
	}
	return opType
}

// formatChildren formats a list of child nodes.
func formatChildren(b *strings.Builder, children []*lens.Node, indent int, rootType string) {
	if len(children) == 0 {
		return
	}

	// Sort children by name for deterministic output
	sorted := make([]*lens.Node, len(children))
	copy(sorted, children)
	sort.Slice(sorted, func(i, j int) bool {
		// Put zero-value fields last
		iZero := sorted[i].IsZero()
		jZero := sorted[j].IsZero()
		if iZero != jZero {
			return !iZero // non-zero first
		}
		return sorted[i].Name < sorted[j].Name
	})

	for _, child := range sorted {
		// Skip zero values for cleaner output, unless it's a key field
		if child.IsZero() && !isKeyField(child.Name) {
			continue
		}
		childStr := formatNode(child, indent, false, rootType)
		lines := strings.Split(strings.TrimSpace(childStr), "\n")
		for _, line := range lines {
			b.WriteString(line + "\n")
		}
	}
}

// isKeyField returns true for fields that should be shown even when zero.
func isKeyField(name string) bool {
	keyFields := map[string]bool{
		"Type":          true,
		"V":             true,
		"SourceAccount": true,
		"Fee":           true,
		"SeqNum":        true,
		"Operations":    true,
		"Memo":          true,
		"Signatures":    true,
		"TimeBounds":    true,
	}
	return keyFields[name]
}

// formatScalar formats a scalar value.
func formatScalar(name string, val any) string {
	if val == nil {
		return "(nil)"
	}

	// Apply specific formatting based on field name
	lowerName := strings.ToLower(name)

	// Check for DataValue early - handle both string and []byte
	// Use Contains to match field names like "DataValue" or nested paths
	if strings.Contains(lowerName, "datavalue") {
		switch v := val.(type) {
		case string:
			return formatDataValue(v)
		case []byte:
			if len(v) == 0 {
				return "empty"
			}
			// Try to decode as string
			str := string(v)
			if isPrintable(str) {
				return fmt.Sprintf("%q", str)
			}
			if len(v) <= 32 {
				return fmt.Sprintf("%x", v)
			}
			return fmt.Sprintf("%x... (%d bytes)", v[:16], len(v))
		}
	}

	switch v := val.(type) {
	case int64:
		if strings.Contains(lowerName, "amount") ||
			strings.Contains(lowerName, "balance") ||
			strings.Contains(lowerName, "reserve") ||
			strings.Contains(lowerName, "fee") ||
			strings.Contains(lowerName, "minbalance") ||
			strings.Contains(lowerName, "price") {
			xlm := float64(v) / 10_000_000
			return fmt.Sprintf("%d (%.7f XLM)", v, xlm)
		}
		return fmt.Sprintf("%d", v)
	case uint64:
		if strings.Contains(lowerName, "amount") ||
			strings.Contains(lowerName, "balance") ||
			strings.Contains(lowerName, "reserve") {
			xlm := float64(v) / 10_000_000
			return fmt.Sprintf("%d (%.7f XLM)", v, xlm)
		}
		return fmt.Sprintf("%d", v)
	case uint32:
		return fmt.Sprintf("%d", v)
	case int32:
		return fmt.Sprintf("%d", v)
	case string:
		// Check if it's a result code - convert to short form (lower camelCase)
		if strings.Contains(lowerName, "code") || strings.Contains(lowerName, "result") {
			return formatResultCodeLowerCamel(v)
		}
		// Check if it's an operation type
		if strings.Contains(lowerName, "type") && strings.HasPrefix(v, "OperationType") {
			return formatOperationType(v)
		}
		// Check if it's a liquidity pool type
		if strings.Contains(lowerName, "type") && strings.HasPrefix(v, "LiquidityPoolType") {
			return formatLiquidityPoolType(v)
		}
		// Check if it looks like an address (starts with G)
		if len(v) >= 56 && (v[0] == 'G' || v[0] == 'M') && strings.Contains(name, "Account") {
			return v
		}
		// Check if it's a hash
		if len(v) == 64 && isHex(v) {
			return v[:16] + "..."
		}
		return fmt.Sprintf("%q", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []byte:
		if len(v) == 0 {
			return "empty"
		}
		if len(v) <= 32 {
			return fmt.Sprintf("%x", v)
		}
		return fmt.Sprintf("%x... (%d bytes)", v[:16], len(v))
	}

	return fmt.Sprintf("%v", val)
}

// formatResultCodeLowerCamel converts long XDR constant names to short lowerCamelCase codes.
func formatResultCodeLowerCamel(code string) string {
	// Transaction result codes
	if strings.HasPrefix(code, "TransactionResultCode") {
		return toLowerCamelCase(strings.TrimPrefix(code, "TransactionResultCode"))
	}
	// Operation result codes
	if strings.HasPrefix(code, "OperationResultCode") {
		return toLowerCamelCase(strings.TrimPrefix(code, "OperationResultCode"))
	}
	// Specific operation result codes
	prefixes := []string{
		"PaymentResultCode",
		"PathPaymentStrictReceiveResultCode",
		"PathPaymentStrictSendResultCode",
		"ManageSellOfferResultCode",
		"ManageBuyOfferResultCode",
		"CreatePassiveSellOfferResultCode",
		"ChangeTrustResultCode",
		"AllowTrustResultCode",
		"AccountMergeResultCode",
		"InflationResultCode",
		"ManageDataResultCode",
		"BumpSequenceResultCode",
		"CreateClaimableBalanceResultCode",
		"ClaimClaimableBalanceResultCode",
		"BeginSponsoringFutureReservesResultCode",
		"EndSponsoringFutureReservesResultCode",
		"RevokeSponsorshipResultCode",
		"ClawbackResultCode",
		"ClawbackClaimableBalanceResultCode",
		"SetTrustLineFlagsResultCode",
		"LiquidityPoolDepositResultCode",
		"LiquidityPoolWithdrawResultCode",
		"InvokeHostFunctionResultCode",
		"ExtendFootprintTtlResultCode",
		"RestoreFootprintResultCode",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(code, prefix) {
			return toLowerCamelCase(strings.TrimPrefix(code, prefix))
		}
	}
	return code
}

// formatLiquidityPoolType converts long pool type names to short form.
func formatLiquidityPoolType(poolType string) string {
	if poolType == "LiquidityPoolTypeLiquidityPoolConstantProduct" {
		return "constantProduct"
	}
	return poolType
}

// formatDataValue tries to decode hex bytes as a string.
func formatDataValue(hexStr string) string {
	// Try to decode hex first
	if isHex(hexStr) && len(hexStr)%2 == 0 {
		if bytes, err := hex.DecodeString(hexStr); err == nil {
			str := string(bytes)
			if isPrintable(str) {
				return fmt.Sprintf("%q", str)
			}
		}
	}
	// If not valid hex or decoded bytes not printable, return as-is (quoted if short)
	if len(hexStr) < 64 && isPrintable(hexStr) {
		return fmt.Sprintf("%q", hexStr)
	}
	return hexStr
}

// isPrintable checks if a string contains only printable characters.
func isPrintable(s string) bool {
	for _, r := range s {
		if r < 32 || r > 126 {
			return false
		}
	}
	return true
}

// toLowerCamelCase converts snake_case to lowerCamelCase (first letter lowercase).
func toLowerCamelCase(s string) string {
	parts := strings.Split(s, "_")
	for i := range parts {
		if len(parts[i]) > 0 {
			if i == 0 {
				parts[i] = strings.ToLower(parts[i][:1]) + parts[i][1:]
			} else {
				parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
			}
		}
	}
	return strings.Join(parts, "")
}

// toSnakeCase converts camelCase to snake_case.
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteByte(byte(r + 32)) // to lowercase
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// isHex checks if a string is valid hexadecimal.
func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
