# System Prompt Additions for Code Quality

This document establishes code quality standards for all languages used in this project: **Rust, Golang, TypeScript, and JavaScript**.

---

## Universal Code Quality Standards

NEVER write production code that contains:

1. **Unchecked panic/error conditions** - always handle errors explicitly
2. **Memory/resource leaks** - every resource acquisition must have corresponding cleanup
3. **Data corruption potential** - all state transitions must preserve data integrity
4. **Inconsistent error handling patterns** - establish and follow single pattern per language

ALWAYS:

1. **Write comprehensive tests BEFORE implementing features**
2. **Include invariant validation in data structures**
3. **Use proper bounds checking for numeric conversions**
4. **Document known bugs immediately and fix them before continuing**
5. **Implement proper separation of concerns**
6. **Use static analysis tools before considering code complete**

---

## Language-Specific Quality Standards

### Rust

#### ERROR HANDLING:
- Use `Result<T, Error>` for all fallible operations
- Define comprehensive error enums with context
- Never use `unwrap()` or `expect()` in production code paths
- Use `?` operator for error propagation
- Provide meaningful error messages

**DANGEROUS PATTERNS:**
```rust
// NEVER DO THIS - production panic
panic!("This should never happen");

// NEVER DO THIS - unchecked conversion
let id = size as u32; // Can overflow on 64-bit

// NEVER DO THIS - ignoring errors
some_operation().unwrap();

// NEVER DO THIS - leaking resources
let resource = allocate();
// ... no corresponding deallocation
```

**PREFERRED PATTERNS:**
```rust
// DO THIS - proper error handling
fn operation() -> Result<T, MyError> {
    match risky_operation() {
        Ok(value) => Ok(process(value)),
        Err(e) => Err(MyError::from(e)),
    }
}

// DO THIS - safe conversion
let id: u32 = size.try_into()
    .map_err(|_| Error::InvalidSize(size))?;

// DO THIS - explicit error handling
let result = some_operation()
    .map_err(|e| Error::OperationFailed(e))?;

// DO THIS - RAII resource management
struct ResourceManager {
    resource: Resource,
}

impl Drop for ResourceManager {
    fn drop(&mut self) {
        self.resource.cleanup();
    }
}
```

#### MEMORY MANAGEMENT:
- Audit all allocations for corresponding deallocations
- Use RAII patterns consistently
- Prefer borrowing over cloning when possible
- Use `Cow<T>` for conditional cloning
- Test for memory leaks in long-running scenarios

#### TESTING:
```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_normal_operation() {
        // Test typical usage patterns
    }

    #[test]
    fn test_edge_cases() {
        // Test boundary conditions
    }

    #[test]
    fn test_error_conditions() {
        // Test all error paths
    }
}

#[cfg(test)]
mod property_tests {
    use proptest::prelude::*;

    proptest! {
        #[test]
        fn test_invariant_always_holds(input in any::<InputType>()) {
            let result = operation(input);
            assert!(check_invariant(&result));
        }
    }
}
```

---

### Golang

#### ERROR HANDLING:
- Always check returned errors - never ignore them
- Return errors as the last return value: `(T, error)`
- Use custom error types with context
- Wrap errors with additional context using `fmt.Errorf("context: %w", err)`
- Never use `panic()` in normal operation paths

**DANGEROUS PATTERNS:**
```go
// NEVER DO THIS - ignoring errors
result, _ := riskyOperation()

// NEVER DO THIS - production panic
if err != nil {
    panic("This should never happen")
}

// NEVER DO THIS - unchecked type assertion
value := interface{}.(ConcreteType)

// NEVER DO THIS - silent error
func operation() {
    err := doSomething()
    // error not returned or logged
}

// NEVER DO THIS - resource leak
file, err := os.Open("file.txt")
if err != nil {
    return err
}
// ... no defer file.Close()
```

**PREFERRED PATTERNS:**
```go
// DO THIS - explicit error handling
result, err := riskyOperation()
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}

// DO THIS - safe type assertion
value, ok := interface{}.(ConcreteType)
if !ok {
    return ErrInvalidType
}

// DO THIS - proper resource cleanup
file, err := os.Open("file.txt")
if err != nil {
    return fmt.Errorf("failed to open file: %w", err)
}
defer file.Close()

// DO THIS - comprehensive error context
func operation() error {
    if err := validateInput(); err != nil {
        return fmt.Errorf("input validation failed: %w", err)
    }

    result, err := processData()
    if err != nil {
        return fmt.Errorf("data processing failed: %w", err)
    }

    return nil
}

// DO THIS - custom error types
type ValidationError struct {
    Field   string
    Message string
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation failed for %s: %s", e.Field, e.Message)
}
```

#### RESOURCE MANAGEMENT:
- Always use `defer` for cleanup operations
- Close all resources (files, connections, channels)
- Use context for cancellation and timeouts
- Avoid goroutine leaks - ensure all goroutines can exit

```go
// DO THIS - proper goroutine management
func processWithTimeout(ctx context.Context) error {
    done := make(chan error, 1)

    go func() {
        done <- heavyOperation()
    }()

    select {
    case err := <-done:
        return err
    case <-ctx.Done():
        return ctx.Err()
    }
}

// DO THIS - defer for cleanup
func processData() error {
    mu.Lock()
    defer mu.Unlock()

    conn, err := db.Open()
    if err != nil {
        return err
    }
    defer conn.Close()

    // ... use connection
    return nil
}
```

#### TESTING:
```go
func TestNormalOperation(t *testing.T) {
    result, err := operation(validInput)
    if err != nil {
        t.Fatalf("expected no error, got %v", err)
    }

    if result != expected {
        t.Errorf("expected %v, got %v", expected, result)
    }
}

func TestErrorConditions(t *testing.T) {
    _, err := operation(invalidInput)
    if err == nil {
        t.Fatal("expected error, got nil")
    }

    var validationErr *ValidationError
    if !errors.As(err, &validationErr) {
        t.Errorf("expected ValidationError, got %T", err)
    }
}

func TestTableDriven(t *testing.T) {
    tests := []struct {
        name    string
        input   Input
        want    Output
        wantErr bool
    }{
        {"valid input", validInput, expectedOutput, false},
        {"invalid input", invalidInput, Output{}, true},
        {"edge case", edgeCase, edgeOutput, false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := operation(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if !reflect.DeepEqual(got, tt.want) {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}

// DO THIS - benchmark critical paths
func BenchmarkOperation(b *testing.B) {
    input := prepareInput()
    b.ResetTimer()

    for i := 0; i < b.N; i++ {
        operation(input)
    }
}
```

#### CONCURRENCY:
- Protect shared state with mutexes or channels
- Prefer channels for communication
- Document all goroutines and their lifecycle
- Use context for cancellation
- Test for race conditions with `-race` flag

---

### TypeScript

#### ERROR HANDLING:
- Use explicit error types
- Handle Promise rejections properly
- Never silently catch and ignore errors
- Use custom Error classes for different error types
- Validate input at API boundaries

**DANGEROUS PATTERNS:**
```typescript
// NEVER DO THIS - silent error
try {
    riskyOperation();
} catch (e) {
    // Error silently ignored
}

// NEVER DO THIS - unhandled promise
asyncOperation(); // Promise rejection not handled

// NEVER DO THIS - any type abuse
function process(data: any): any {
    return data.someProp; // No type safety
}

// NEVER DO THIS - non-null assertion without validation
const value = maybeNull!.property;

// NEVER DO THIS - ignoring errors
await operation().catch(() => {});
```

**PREFERRED PATTERNS:**
```typescript
// DO THIS - explicit error handling
class ValidationError extends Error {
    constructor(
        message: string,
        public field: string,
        public code: string
    ) {
        super(message);
        this.name = 'ValidationError';
    }
}

// DO THIS - type-safe error handling
type Result<T, E = Error> =
    | { success: true; value: T }
    | { success: false; error: E };

function operation(input: Input): Result<Output> {
    if (!isValid(input)) {
        return {
            success: false,
            error: new ValidationError('Invalid input', 'input', 'INVALID')
        };
    }

    return { success: true, value: process(input) };
}

// DO THIS - proper async error handling
async function processAsync(input: Input): Promise<Output> {
    try {
        const validated = await validateInput(input);
        const result = await processData(validated);
        return result;
    } catch (error) {
        if (error instanceof ValidationError) {
            throw new Error(`Validation failed: ${error.message}`);
        }
        throw new Error(`Processing failed: ${error}`);
    }
}

// DO THIS - null checking with type guards
function processValue(value: string | null): string {
    if (value === null) {
        throw new Error('Value cannot be null');
    }
    return value.toUpperCase();
}

// DO THIS - proper promise handling
async function main() {
    try {
        await asyncOperation();
    } catch (error) {
        console.error('Operation failed:', error);
        throw error;
    }
}
```

#### RESOURCE MANAGEMENT:
- Clean up event listeners
- Cancel pending requests when components unmount
- Close WebSocket connections properly
- Clear timeouts and intervals

```typescript
// DO THIS - cleanup in React
useEffect(() => {
    const controller = new AbortController();

    async function fetchData() {
        try {
            const response = await fetch(url, { signal: controller.signal });
            const data = await response.json();
            setData(data);
        } catch (error) {
            if (error.name !== 'AbortError') {
                console.error('Fetch failed:', error);
            }
        }
    }

    fetchData();

    return () => {
        controller.abort();
    };
}, [url]);

// DO THIS - cleanup subscriptions
class EventManager {
    private listeners: Map<string, Set<Function>> = new Map();

    subscribe(event: string, handler: Function): () => void {
        if (!this.listeners.has(event)) {
            this.listeners.set(event, new Set());
        }
        this.listeners.get(event)!.add(handler);

        // Return cleanup function
        return () => {
            this.listeners.get(event)?.delete(handler);
        };
    }

    cleanup(): void {
        this.listeners.clear();
    }
}
```

#### TYPE SAFETY:
- Avoid `any` type - use `unknown` if type is truly unknown
- Enable strict mode in tsconfig.json
- Use discriminated unions for complex types
- Validate external data at runtime

```typescript
// DO THIS - strict type checking
interface User {
    id: string;
    name: string;
    email: string;
}

function isUser(value: unknown): value is User {
    return (
        typeof value === 'object' &&
        value !== null &&
        'id' in value &&
        'name' in value &&
        'email' in value &&
        typeof (value as User).id === 'string' &&
        typeof (value as User).name === 'string' &&
        typeof (value as User).email === 'string'
    );
}

// DO THIS - discriminated unions
type ApiResponse<T> =
    | { status: 'success'; data: T }
    | { status: 'error'; error: string }
    | { status: 'loading' };

function handleResponse<T>(response: ApiResponse<T>): void {
    switch (response.status) {
        case 'success':
            console.log(response.data); // Type-safe access
            break;
        case 'error':
            console.error(response.error); // Type-safe access
            break;
        case 'loading':
            console.log('Loading...');
            break;
    }
}
```

#### TESTING:
```typescript
describe('operation', () => {
    it('should handle valid input', () => {
        const result = operation(validInput);
        expect(result).toEqual(expectedOutput);
    });

    it('should throw error for invalid input', () => {
        expect(() => operation(invalidInput)).toThrow(ValidationError);
    });

    it('should handle edge cases', () => {
        const result = operation(edgeCase);
        expect(result).toBeDefined();
    });
});

describe('async operation', () => {
    it('should resolve with data', async () => {
        const result = await asyncOperation(validInput);
        expect(result).toEqual(expectedOutput);
    });

    it('should reject with error', async () => {
        await expect(asyncOperation(invalidInput))
            .rejects.toThrow(ValidationError);
    });
});

// DO THIS - test error boundaries
describe('ErrorBoundary', () => {
    it('should catch errors', () => {
        const spy = jest.spyOn(console, 'error').mockImplementation();

        render(
            <ErrorBoundary fallback={<div>Error</div>}>
                <ThrowError />
            </ErrorBoundary>
        );

        expect(screen.getByText('Error')).toBeInTheDocument();
        spy.mockRestore();
    });
});
```

---

### JavaScript

#### ERROR HANDLING:
- Always handle promise rejections
- Use try-catch for synchronous errors
- Create custom error classes
- Log errors with context
- Never suppress errors silently

**DANGEROUS PATTERNS:**
```javascript
// NEVER DO THIS - silent error
try {
    riskyOperation();
} catch (e) {}

// NEVER DO THIS - unhandled rejection
asyncOperation();

// NEVER DO THIS - callback without error handling
fs.readFile('file.txt', (err, data) => {
    console.log(data); // err not checked
});

// NEVER DO THIS - swallowing errors in promise chain
promise.then(result => result).catch(() => {});
```

**PREFERRED PATTERNS:**
```javascript
// DO THIS - custom error classes
class ValidationError extends Error {
    constructor(message, field, code) {
        super(message);
        this.name = 'ValidationError';
        this.field = field;
        this.code = code;
    }
}

// DO THIS - explicit error handling
function operation(input) {
    if (!isValid(input)) {
        throw new ValidationError(
            'Invalid input',
            'input',
            'INVALID_INPUT'
        );
    }

    try {
        return processData(input);
    } catch (error) {
        throw new Error(`Processing failed: ${error.message}`);
    }
}

// DO THIS - async/await with error handling
async function processAsync(input) {
    try {
        const validated = await validateInput(input);
        const result = await processData(validated);
        return result;
    } catch (error) {
        console.error('Processing failed:', error);
        throw error;
    }
}

// DO THIS - callback error handling
fs.readFile('file.txt', (err, data) => {
    if (err) {
        console.error('Failed to read file:', err);
        return;
    }
    console.log(data);
});

// DO THIS - promise error handling
promise
    .then(result => processResult(result))
    .catch(error => {
        console.error('Operation failed:', error);
        throw error;
    });
```

#### RESOURCE MANAGEMENT:
- Clean up timers and intervals
- Remove event listeners when done
- Close connections and streams
- Cancel pending operations

```javascript
// DO THIS - cleanup patterns
class ResourceManager {
    constructor() {
        this.timers = new Set();
        this.listeners = new Map();
    }

    setTimeout(callback, delay) {
        const id = setTimeout(() => {
            callback();
            this.timers.delete(id);
        }, delay);
        this.timers.add(id);
        return id;
    }

    addEventListener(element, event, handler) {
        element.addEventListener(event, handler);

        if (!this.listeners.has(element)) {
            this.listeners.set(element, new Map());
        }
        if (!this.listeners.get(element).has(event)) {
            this.listeners.get(element).set(event, new Set());
        }
        this.listeners.get(element).get(event).add(handler);
    }

    cleanup() {
        // Clear all timers
        for (const id of this.timers) {
            clearTimeout(id);
        }
        this.timers.clear();

        // Remove all listeners
        for (const [element, events] of this.listeners) {
            for (const [event, handlers] of events) {
                for (const handler of handlers) {
                    element.removeEventListener(event, handler);
                }
            }
        }
        this.listeners.clear();
    }
}
```

#### INPUT VALIDATION:
- Validate all external input
- Check types at runtime
- Sanitize user input
- Handle null/undefined explicitly

```javascript
// DO THIS - input validation
function validateUser(data) {
    if (!data || typeof data !== 'object') {
        throw new ValidationError('Invalid data', 'data', 'INVALID_TYPE');
    }

    if (typeof data.id !== 'string' || data.id.trim() === '') {
        throw new ValidationError('Invalid ID', 'id', 'INVALID_ID');
    }

    if (typeof data.name !== 'string' || data.name.trim() === '') {
        throw new ValidationError('Invalid name', 'name', 'INVALID_NAME');
    }

    if (typeof data.email !== 'string' || !isValidEmail(data.email)) {
        throw new ValidationError('Invalid email', 'email', 'INVALID_EMAIL');
    }

    return {
        id: data.id.trim(),
        name: data.name.trim(),
        email: data.email.toLowerCase().trim()
    };
}

// DO THIS - safe property access
function getNestedValue(obj, path, defaultValue) {
    const keys = path.split('.');
    let current = obj;

    for (const key of keys) {
        if (current === null || current === undefined || !(key in current)) {
            return defaultValue;
        }
        current = current[key];
    }

    return current;
}
```

#### TESTING:
```javascript
describe('operation', () => {
    test('handles valid input', () => {
        const result = operation(validInput);
        expect(result).toEqual(expectedOutput);
    });

    test('throws error for invalid input', () => {
        expect(() => operation(invalidInput)).toThrow(ValidationError);
    });

    test('handles edge cases', () => {
        const result = operation(edgeCase);
        expect(result).toBeDefined();
    });
});

describe('async operation', () => {
    test('resolves with data', async () => {
        const result = await asyncOperation(validInput);
        expect(result).toEqual(expectedOutput);
    });

    test('rejects with error', async () => {
        await expect(asyncOperation(invalidInput))
            .rejects.toThrow(ValidationError);
    });
});

// DO THIS - mock external dependencies
describe('API client', () => {
    beforeEach(() => {
        fetch.mockClear();
    });

    test('fetches data successfully', async () => {
        fetch.mockResolvedValueOnce({
            ok: true,
            json: async () => ({ data: 'test' })
        });

        const result = await fetchData();
        expect(result).toEqual({ data: 'test' });
    });

    test('handles fetch errors', async () => {
        fetch.mockRejectedValueOnce(new Error('Network error'));

        await expect(fetchData()).rejects.toThrow('Network error');
    });
});
```

---

## Development Process Guards

### TESTING REQUIREMENTS:
- Write failing tests first, then implement to make them pass
- Never commit code with failing tests
- Include edge case and boundary condition tests
- Test error paths explicitly
- Validate all assumptions with tests
- Run tests before committing

### ARCHITECTURE REQUIREMENTS:
- Explicit error handling - no hidden panics or uncaught errors
- Resource safety - all resources must be properly cleaned up
- Performance conscious - avoid unnecessary allocations/operations
- API design - consistent patterns across all public interfaces
- Separation of concerns - single responsibility per module/function

### REVIEW CHECKPOINTS:

Before marking any code complete, verify:

1. **No compilation/linting warnings**
2. **All tests pass (including integration/stress tests)**
3. **Memory/resource usage is bounded and predictable**
4. **No data corruption potential in any code path**
5. **Error handling is comprehensive and consistent**
6. **Code is modular and maintainable**
7. **Documentation matches implementation**
8. **Performance is acceptable**

### STATIC ANALYSIS TOOLS:

- **Rust**: clippy, miri, cargo-audit
- **Golang**: golangci-lint, go vet, staticcheck, gosec
- **TypeScript**: ESLint, TypeScript compiler strict mode, prettier
- **JavaScript**: ESLint, prettier, JSHint

Run these tools in CI/CD and fix all warnings before merging.

---

## Documentation Standards

### CODE DOCUMENTATION:
- Document all public APIs with examples
- Explain complex algorithms and data structures
- Document invariants and preconditions
- Include safety notes for unsafe code
- Provide usage examples

### Rust Documentation:
```rust
/// Inserts a key-value pair into the tree.
///
/// # Arguments
/// * `key` - The key to insert (must implement Ord)
/// * `value` - The value to associate with the key
///
/// # Returns
/// * `Ok(old_value)` if key existed (returns old value)
/// * `Ok(None)` if key was newly inserted
/// * `Err(Error::InvalidKey)` if key violates constraints
///
/// # Examples
/// ```
/// let mut tree = BPlusTree::new(4)?;
/// assert_eq!(tree.insert(1, "value")?, None);
/// assert_eq!(tree.insert(1, "new")?, Some("value"));
/// ```
///
/// # Panics
/// Never panics - all error conditions return Result
pub fn insert(&mut self, key: K, value: V) -> Result<Option<V>, Error> {
    // Implementation
}
```

### Golang Documentation:
```go
// Insert adds a key-value pair to the tree.
//
// If the key already exists, the old value is returned.
// Returns an error if the key violates tree constraints.
//
// Example:
//   tree := NewBPlusTree(4)
//   oldValue, err := tree.Insert(1, "value")
//   if err != nil {
//       return err
//   }
func (t *BPlusTree) Insert(key int, value interface{}) (interface{}, error) {
    // Implementation
}
```

### TypeScript Documentation:
```typescript
/**
 * Inserts a key-value pair into the tree.
 *
 * @param key - The key to insert
 * @param value - The value to associate with the key
 * @returns The old value if key existed, undefined otherwise
 * @throws {ValidationError} If key violates constraints
 *
 * @example
 * ```ts
 * const tree = new BPlusTree<number, string>(4);
 * const oldValue = tree.insert(1, "value");
 * ```
 */
public insert(key: number, value: string): string | undefined {
    // Implementation
}
```

### JavaScript Documentation:
```javascript
/**
 * Inserts a key-value pair into the tree.
 *
 * @param {number} key - The key to insert
 * @param {*} value - The value to associate with the key
 * @returns {*} The old value if key existed, undefined otherwise
 * @throws {ValidationError} If key violates constraints
 *
 * @example
 * const tree = new BPlusTree(4);
 * const oldValue = tree.insert(1, "value");
 */
insert(key, value) {
    // Implementation
}
```

---

## Summary

This system prompt establishes comprehensive quality standards that must be followed for all code in this project. By following these guidelines:

1. **Prevent critical bugs** through explicit error handling
2. **Ensure resource safety** with proper cleanup patterns
3. **Maintain data integrity** through invariant validation
4. **Enable maintainability** with clear documentation
5. **Guarantee correctness** with comprehensive testing

These standards apply to all languages used in the project and must be followed without exception.
