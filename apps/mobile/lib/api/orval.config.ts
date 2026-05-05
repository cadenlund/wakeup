// Orval generates one React-Query hook file per OpenAPI tag from
// the backend's swagger.json. Re-run with `bunx orval` (or
// `just gen-client` which now chains it).
//
// Tags-split mode: hooks go under `hooks/<tag>/`, with a per-tag
// barrel for clean imports — `import { useGetMe } from '@/lib/api/hooks/auth';`
// Schemas land under `model/` so consumer code never imports the
// huge `schema.ts`.
//
// Mutator: every hook routes through `orvalMutator` → `apiFetch` so
// cookies, idempotency keys, and error→toast handling are centralised.
import * as path from 'node:path';

import { defineConfig } from 'orval';

// `just gen-client` writes the OpenAPI 3 conversion to this stable
// path; orval consumes it directly so we don't redo the swagger 2 → 3
// conversion here.
const openapiPath = path.resolve(__dirname, '../../../../docs/openapi/openapi.json');

export default defineConfig({
  wakeup: {
    input: {
      target: openapiPath,
    },
    output: {
      target: path.resolve(__dirname, './hooks/index.ts'),
      schemas: path.resolve(__dirname, './model'),
      mode: 'tags-split',
      client: 'react-query',
      httpClient: 'fetch',
      override: {
        mutator: {
          path: path.resolve(__dirname, './orval-mutator.ts'),
          name: 'orvalMutator',
        },
        query: {
          useQuery: true,
          useMutation: true,
        },
      },
    },
  },
});
