#!/bin/bash
# Script to concatenate all Go files for LLM review
# Usage: ./scripts/concat_go_code.sh [directory] [output_file] [options]
# Example: ./scripts/concat_go_code.sh pkg code_dump.txt
# Options:
#   --include-tests     Include test files (excluded by default)
#   --include-sum       Include go.sum files (excluded by default)
#   --max-size KB       Maximum file size in KB (default: 500, 0 = no limit)

set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
INCLUDE_TESTS=false
INCLUDE_SUM=false
MAX_SIZE_KB=500  # Default: exclude files larger than 500KB

# Parse arguments
BASE_DIR=""
OUTPUT_FILE=""
while [[ $# -gt 0 ]]; do
  case $1 in
    --include-tests)
      INCLUDE_TESTS=true
      shift
      ;;
    --include-sum)
      INCLUDE_SUM=true
      shift
      ;;
    --max-size)
      MAX_SIZE_KB="$2"
      shift 2
      ;;
    -*)
      echo -e "${YELLOW}Unknown option: $1${NC}" >&2
      echo "Usage: $0 [directory] [output_file] [--include-tests] [--include-sum] [--max-size KB]"
      exit 1
      ;;
    *)
      if [ -z "$BASE_DIR" ]; then
        BASE_DIR="$1"
      elif [ -z "$OUTPUT_FILE" ]; then
        OUTPUT_FILE="$1"
      fi
      shift
      ;;
  esac
done

# Set defaults if not provided
BASE_DIR="${BASE_DIR:-.}"
OUTPUT_FILE="${OUTPUT_FILE:-code_dump.txt}"

# Convert Windows-style paths to Unix-style for Git Bash
# Strip newlines and convert backslashes to forward slashes
BASE_DIR=$(echo "$BASE_DIR" | sed 's|\\|/|g' | tr -d '\r\n')
OUTPUT_FILE=$(echo "$OUTPUT_FILE" | sed 's|\\|/|g' | tr -d '\r\n')

# Validate directory exists
if [ ! -d "$BASE_DIR" ]; then
  echo -e "${YELLOW}Error: Directory not found: $BASE_DIR${NC}" >&2
  exit 1
fi

# Get absolute paths for better handling
# Get absolute path and strip any newlines/carriage returns
if [ -d "$BASE_DIR" ]; then
  BASE_DIR=$(cd "$BASE_DIR" && pwd 2>/dev/null | tr -d '\r\n' || echo "$BASE_DIR")
  # Convert to forward slashes for cross-platform compatibility
  BASE_DIR=$(echo "$BASE_DIR" | sed 's|\\|/|g' | tr -d '\r\n')
fi

echo -e "${BLUE}=== Go Code Concatenation Script ===${NC}"
echo "Base directory: $BASE_DIR"
echo "Output file: $OUTPUT_FILE"
echo "Include tests: $INCLUDE_TESTS"
echo "Include go.sum: $INCLUDE_SUM"
echo "Max file size: ${MAX_SIZE_KB}KB"
echo ""

# Temporary file to collect file paths
TEMP_FILE=$(mktemp)
trap "rm -f '$TEMP_FILE'" EXIT

# Find all .go files, go.mod, go.sum, and .proto files, excluding vendor/
echo -e "${BLUE}Finding Go files...${NC}"

# Build find command for .go files
GO_FIND_CMD="find \"$BASE_DIR\" -type f -name \"*.go\" ! -path \"*/vendor/*\" ! -path \"*/.git/*\""

# Exclude test files by default
if [ "$INCLUDE_TESTS" = false ]; then
  GO_FIND_CMD="$GO_FIND_CMD ! -name \"*_test.go\""
fi

eval "$GO_FIND_CMD | sort >> \"$TEMP_FILE\""

# Find go.mod files
find "$BASE_DIR" -type f -name "go.mod" ! -path "*/vendor/*" ! -path "*/.git/*" | sort >> "$TEMP_FILE"

# Find go.sum files (excluded by default - can be very large)
if [ "$INCLUDE_SUM" = true ]; then
  find "$BASE_DIR" -type f -name "go.sum" ! -path "*/vendor/*" ! -path "*/.git/*" | sort >> "$TEMP_FILE"
fi

# Find .proto files (Protocol Buffer files)
find "$BASE_DIR" -type f -name "*.proto" ! -path "*/vendor/*" ! -path "*/.git/*" | sort >> "$TEMP_FILE"

# Count files
FILE_COUNT=$(wc -l < "$TEMP_FILE" | tr -d ' ')
if [ "$FILE_COUNT" -eq 0 ]; then
  echo -e "${YELLOW}Warning: No Go files found in $BASE_DIR${NC}" >&2
  exit 1
fi

echo "Found $FILE_COUNT files"
echo ""

# Create output file with header
{
  echo "// ============================================================================"
  echo "// Go/Proto Code Dump - goquota"
  echo "// ============================================================================"
  echo "// Directory: $BASE_DIR"
  echo "// Generated: $(date)"
  echo "// Files found: $FILE_COUNT"
  echo "// Options: include-tests=$INCLUDE_TESTS, include-sum=$INCLUDE_SUM, max-size=${MAX_SIZE_KB}KB"
  echo "// ============================================================================"
  echo "//"
  echo "// Repository Structure:"
  echo "// - pkg/           : Core library code (goquota, api, billing)"
  echo "// - middleware/    : Framework integrations (gin, echo, fiber, http)"
  echo "// - storage/       : Storage adapters (redis, postgres, firestore, memory)"
  echo "// - examples/      : Example applications"
  echo "// ============================================================================"
  echo ""
} > "$OUTPUT_FILE"

# Process each file
CURRENT_FILE=0
INCLUDED_FILE_COUNT=0
while IFS= read -r FILE_PATH; do
  CURRENT_FILE=$((CURRENT_FILE + 1))
  
  # Get relative path from base directory
  REL_PATH="${FILE_PATH#$BASE_DIR/}"
  
  # Check file size and skip if too large (if MAX_SIZE_KB > 0)
  if [ "$MAX_SIZE_KB" != "0" ]; then
    FILE_SIZE_BYTES=$(wc -c < "$FILE_PATH" 2>/dev/null || echo "0")
    FILE_SIZE_KB=$((FILE_SIZE_BYTES / 1024))
    if [ "$FILE_SIZE_KB" -gt "$MAX_SIZE_KB" ]; then
      echo -e "${YELLOW}[$CURRENT_FILE/$FILE_COUNT] Skipping large file (${FILE_SIZE_KB}KB > ${MAX_SIZE_KB}KB): $REL_PATH${NC}"
      continue
    fi
  fi
  
  # Determine comment style based on file extension
  if [[ "$FILE_PATH" == *.mod ]] || [[ "$FILE_PATH" == *.sum ]]; then
    COMMENT_PREFIX="#"
  else
    COMMENT_PREFIX="//"
  fi
  
  # Determine file extension for display
  if [[ "$FILE_PATH" == *.go ]]; then
    # Determine if it's a test file
    if [[ "$FILE_PATH" == *_test.go ]]; then
      FILE_TYPE="Go Test"
    else
      FILE_TYPE="Go"
    fi
  elif [[ "$FILE_PATH" == *.mod ]]; then
    FILE_TYPE="Go Module"
  elif [[ "$FILE_PATH" == *.sum ]]; then
    FILE_TYPE="Go Sum"
  elif [[ "$FILE_PATH" == *.proto ]]; then
    FILE_TYPE="Protocol Buffer"
  else
    FILE_TYPE="Unknown"
  fi
  
  echo -e "${GREEN}[$CURRENT_FILE/$FILE_COUNT]${NC} Adding $FILE_TYPE: $REL_PATH"
  INCLUDED_FILE_COUNT=$((INCLUDED_FILE_COUNT + 1))
  
  # Add file separator comment
  {
    echo ""
    echo "$COMMENT_PREFIX ============================================================================"
    echo "$COMMENT_PREFIX FILE: $REL_PATH"
    echo "$COMMENT_PREFIX Type: $FILE_TYPE"
    echo "$COMMENT_PREFIX ============================================================================"
    echo ""
    cat "$FILE_PATH"
  } >> "$OUTPUT_FILE"
  
done < "$TEMP_FILE"

# Add footer
{
  echo ""
  echo "// ============================================================================"
  echo "// End of Code Dump"
  echo "// Total files: $INCLUDED_FILE_COUNT (found $FILE_COUNT, excluded $((FILE_COUNT - INCLUDED_FILE_COUNT)))"
  echo "// ============================================================================"
} >> "$OUTPUT_FILE"

echo ""
echo -e "${GREEN}✓ Successfully concatenated $INCLUDED_FILE_COUNT files to $OUTPUT_FILE (found $FILE_COUNT, excluded $((FILE_COUNT - INCLUDED_FILE_COUNT)))${NC}"
