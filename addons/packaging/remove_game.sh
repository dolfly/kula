#!/bin/bash

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# remove CSS classes
perl -i -0777 -pe 's/\.btn-game\s*\{[^}]*\}\s*//g' "${PROJECT_ROOT}"/internal/web/static/style.css
perl -i -0777 -pe 's/\.btn-game:hover\s*\{[^}]*\}\s*//g' "${PROJECT_ROOT}"/internal/web/static/style.css

# remove dashboard link (comment + multi-line {{if .EasterEgg}} ... {{end}} block)
perl -i -0777 -pe 's/[ \t]*<!-- Game easter egg -->\n//g' "${PROJECT_ROOT}"/internal/web/static/index.html
perl -i -0777 -pe 's/[ \t]*\{\{if \.EasterEgg\}\}.*?\{\{end\}\}\n//gs' "${PROJECT_ROOT}"/internal/web/static/index.html

# remove game files
rm -f "${PROJECT_ROOT}"/internal/web/static/game.*
