#!/usr/bin/env bash
# ==============================================================================
# build_and_test.sh - Local build & test automation script for Ubuntu 22.04
# ==============================================================================
set -euo pipefail

# ANSI color codes for terminal output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info()    { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
log_test()    { echo -e "${YELLOW}[TEST]${NC} $*"; }

# ------------------------------------------------------------------------------
# 1. Environment & Prerequisites Check
# ------------------------------------------------------------------------------
log_info "Verifying prerequisites on Ubuntu 22.04..."

if ! command -v go >/dev/null 2>&1; then
    log_error "Go compiler not found! Please install Go 1.22+ via: sudo snap install go --classic"
    exit 1
fi

GO_VERSION=$(go version | awk '{print $3}')
log_info "Detected Go version: ${GO_VERSION}"

# Create temporary workspace for testing
TMP_DIR=$(mktemp -d /tmp/jsonata-test-XXXXXX)
cleanup() {
    rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

BIN_DIR="./bin"
mkdir -p "${BIN_DIR}"

# ------------------------------------------------------------------------------
# 2. Resolve Upstream Metadata & Build Info
# ------------------------------------------------------------------------------
log_info "Resolving upstream gnata metadata and git information..."

BUILD_DATE=$(date -u +'%Y%m%d-%H%M%S')
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "local")
CLI_VERSION="v1.0.0-dev"

# Extract upstream gnata version
GNATA_VER=$(go list -m -f '{{.Version}}' github.com/recolabs/gnata 2>/dev/null || echo "unknown")
log_info "Upstream gnata module: ${GNATA_VER}"
log_info "Build timestamp:       ${BUILD_DATE}"
log_info "Git commit:            ${GIT_COMMIT}"

LDFLAGS="-s -w \
  -X 'main.Version=${CLI_VERSION}' \
  -X 'main.Commit=${GIT_COMMIT}' \
  -X 'main.BuildDate=${BUILD_DATE}'"

# ------------------------------------------------------------------------------
# 3. Run Go Unit Tests with Race Detector
# ------------------------------------------------------------------------------
log_info "Running Go unit tests with race detection..."
go test -v -race ./...
log_success "Unit tests passed!"

# ------------------------------------------------------------------------------
# 4. Build Native Linux Binary
# ------------------------------------------------------------------------------
HOST_ARCH=$(go env GOARCH)
NATIVE_BIN="${BIN_DIR}/jsonata"

log_info "Compiling native Linux (${HOST_ARCH}) binary with CGO_ENABLED=0..."
CGO_ENABLED=0 go build -trimpath -ldflags="${LDFLAGS}" -o "${NATIVE_BIN}" .

if [ ! -s "${NATIVE_BIN}" ]; then
    log_error "Native binary compilation failed or file is 0 bytes."
    exit 1
fi
log_success "Compiled native binary: ${NATIVE_BIN}"

# ------------------------------------------------------------------------------
# 5. Execute Local Smoke & CLI Integration Tests
# ------------------------------------------------------------------------------
log_info "Executing CLI functionality tests..."

# Test 1: Version Flag
log_test "Flag: --version"
VER_OUT=$("${NATIVE_BIN}" --version)
echo "  -> ${VER_OUT}"
[[ "${VER_OUT}" == *"jsonata version"* ]] || { log_error "Version output mismatch"; exit 1; }

# Test 2: Help Flag
log_test "Flag: --help"
"${NATIVE_BIN}" --help > /dev/null

# Prepare test payloads
SAMPLE_JSON="${TMP_DIR}/order.json"
cat << 'EOF' > "${SAMPLE_JSON}"
{
  "Account": {
    "Account Name": "Firefly",
    "Order": [
      { "OrderID": "order103", "Product": "Bowler Hat", "Quantity": 2, "Price": 28.50 },
      { "OrderID": "order104", "Product": "Cloak", "Quantity": 1, "Price": 107.99 }
    ]
  }
}
EOF

TRANSFORM_FILE="${TMP_DIR}/total.jsonata"
cat << 'EOF' > "${TRANSFORM_FILE}"
$sum(Account.Order.(Price * Quantity))
EOF

# Test 3: Case 1 - Inline Expression + File (-f)
log_test "Case 1: Inline expression with input file"
RES_1=$("${NATIVE_BIN}" "Account.Order[0].Product" -f "${SAMPLE_JSON}")
echo "  -> Expected: \"Bowler Hat\" | Got: ${RES_1}"
[ "${RES_1}" = '"Bowler Hat"' ] || { log_error "Case 1 Failed"; exit 1; }

# Test 4: Case 2 - Stdin Data + Inline Expression
log_test "Case 2: Piped data stream via stdin"
RES_2=$(cat "${SAMPLE_JSON}" | "${NATIVE_BIN}" "Account.Order[1].OrderID")
echo "  -> Expected: \"order104\" | Got: ${RES_2}"
[ "${RES_2}" = '"order104"' ] || { log_error "Case 2 Failed"; exit 1; }

# Test 5: Case 3 - Heavy Expression via stdin + Data File (-f)
log_test "Case 3: Expression redirected from file with -f data"
RES_3=$("${NATIVE_BIN}" -f "${SAMPLE_JSON}" < "${TRANSFORM_FILE}")
echo "  -> Expected: 164.99 | Got: ${RES_3}"
[ "${RES_3}" = "164.99" ] || { log_error "Case 3 Failed"; exit 1; }

# Test 6: Undefined Result Handling (Should output nothing without error)
log_test "Edge Case: Undefined path evaluation"
RES_UNDEF=$("${NATIVE_BIN}" "Account.NonExistent" -f "${SAMPLE_JSON}")
[ -z "${RES_UNDEF}" ] || { log_error "Expected empty output for undefined path"; exit 1; }

# Test 7: Error handling for missing input data
log_test "Error Handling: Expression provided but no stdin or -f flag"
set +e
"${NATIVE_BIN}" "Account.Order" >/dev/null 2>&1
STATUS=$?
set -e
if [ ${STATUS} -eq 0 ]; then
    log_error "Expected non-zero exit code when no input data is provided"; exit 1;
fi

log_success "All CLI functional smoke tests passed!"

# ------------------------------------------------------------------------------
# 6. Cross-Compile Target Matrix (Including Primary: Windows on ARM64)
# ------------------------------------------------------------------------------
log_info "Verifying cross-compilation targets..."

TARGETS=(
  "windows/arm64/jsonata-windows-arm64.exe"
  "windows/amd64/jsonata-windows-amd64.exe"
  "linux/arm64/jsonata-linux-arm64"
  "darwin/arm64/jsonata-darwin-arm64"
)

for TARGET in "${TARGETS[@]}"; do
    IFS="/" read -r TARGET_OS TARGET_ARCH TARGET_OUT <<< "${TARGET}"
    log_info "  -> Building ${TARGET_OS}/${TARGET_ARCH}..."
    
    CGO_ENABLED=0 GOOS="${TARGET_OS}" GOARCH="${TARGET_ARCH}" go build \
      -trimpath \
      -ldflags="${LDFLAGS}" \
      -o "${BIN_DIR}/${TARGET_OUT}" .
      
    if [ ! -s "${BIN_DIR}/${TARGET_OUT}" ]; then
        log_error "Cross-compile failed for ${TARGET_OS}/${TARGET_ARCH}"
        exit 1
    fi
done

log_success "Cross-compilation succeeded for all targets!"

# ------------------------------------------------------------------------------
# 7. Summary
# ------------------------------------------------------------------------------
echo ""
echo -e "${GREEN}================================================================${NC}"
echo -e "${GREEN}  Local Build & Verification Complete! Ready for GitHub CI.     ${NC}"
echo -e "${GREEN}================================================================${NC}"
echo -e "Compiled Binaries in ${BIN_DIR}/:"
ls -lh "${BIN_DIR}"