# Testing multi-operation transaction pipelines

A Stellar transaction executes its operations in order and atomically. A
useful test checks the exact sequence, important operation fields, and source
accounts rather than only checking that the transaction contains a few
expected operation types.

These examples follow the APIs in
[`pkg/focus/op_order.go`](../../pkg/focus/op_order.go),
[`pkg/focus/operations.go`](../../pkg/focus/operations.go), and
[`pkg/focus/fields.go`](../../pkg/focus/fields.go).

## Assert the ordered operation sequence

`AssertOperationSequence` takes XDR enum values, while `AssertOperationTypes`
takes the snake_case names printed by the tool. Both check order and exact
length:

```go
focus.AssertOperationSequence(t, env, []xdr.OperationType{
    xdr.OperationTypeBeginSponsoringFutureReserves,
    xdr.OperationTypeCreateAccount,
    xdr.OperationTypeEndSponsoringFutureReserves,
})

focus.AssertOperationTypes(t, env,
    "begin_sponsoring_future_reserves",
    "create_account",
    "end_sponsoring_future_reserves",
)
```

`AssertOperationsEqual` compares two complete `Operation` values. It is
useful when the test already has the expected XDR operation and wants to pin
all of its fields, not just its type:

```go
focus.AssertOperationsEqual(t, wantCreateAccountOp, gotCreateAccountOp)
```

For a transaction pipeline, combine sequence checks with field assertions at
the operation indexes that matter:

```go
focus.AssertCreateAccount(t, env, 1, newAccount, 20_0000000)
```

Amounts passed to operation assertions are in stroops.

## Check explicit and inherited operation sources

An operation with no `SourceAccount` inherits the transaction's source
account. `AssertOperationSourceAccount` checks whether an operation declares
its own source: an empty `want` asserts that no per-operation source is set;
a non-empty `want` checks the explicit source address.

```go
focus.AssertSourceAccount(t, env, transactionSource)

// Operation 0 inherits transactionSource because it has no source of its own.
focus.AssertOperationSourceAccount(t, env, 0, "")

// Operation 1 explicitly overrides the source.
focus.AssertOperationSourceAccount(t, env, 1, sponsoringAccount)

// Operation 2 also inherits transactionSource.
focus.AssertOperationSourceAccount(t, env, 2, "")
```

Pairing the transaction-level assertion with the empty per-operation checks
proves the effective source for inherited operations. The helper deliberately
does not treat `want == ""` as a request to compare an operation's effective
source with the transaction source.

## Match operations by position

`AssertOperationsMatch` applies one matcher per operation, in sequence, and
requires the number of operations to equal the number of matchers. A `nil`
matcher skips field matching at that position, which is useful when only a
few positions matter:

```go
focus.AssertOperationsMatch(t, env,
    nil, // operation 0 is present, but not otherwise constrained here
    focus.MatchPayment(recipient, 10_0000000),
    nil,
)
```

The example still requires exactly three operations and checks that operation
1 is a payment to `recipient` for the expected amount. Matchers such as
`MatchPayment`, `MatchSetOptions`, and `MatchInvokeHost` inspect operation
fields; they do not search the transaction for a matching operation at an
arbitrary index.
