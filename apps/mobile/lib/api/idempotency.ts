// Idempotency-key helpers per spec §4.7. Every POST/PATCH/PUT
// mutation generates a UUID v7 client-side; retries reuse the same
// key so the backend de-dupes. v7 is preferred over v4 because it's
// time-ordered, which makes server-side log correlation easier.
//
// Implementation note: we don't use the `uuid` package because its
// v7 generator depends on `crypto.getRandomValues`, which Hermes
// doesn't expose on iOS without a native polyfill. Idempotency keys
// don't need cryptographic security — only enough randomness that
// two mutations issued in the same millisecond collide vanishingly
// often. Math.random() gives ~52 bits per call; we burn 74 bits per
// key, which is more than enough.

const HEX = '0123456789abcdef';

function randomHex(nibbles: number): string {
  let out = '';
  for (let i = 0; i < nibbles; i++) {
    out += HEX[(Math.random() * 16) | 0];
  }
  return out;
}

// UUID v7 layout (RFC 9562 §5.7):
//   ttttttttttttttttttttttttttttttttttttttttttttttttttRRRRRRRRRRRR (48b unix ms)
//   0111 RRRRRRRRRRRR  (version 7 + 12 random)
//   10 RRRRRRRRRRRR... (variant 10 + 62 random)
// Hex form: 8-4-4-4-12.
export function newIdempotencyKey(): string {
  const ms = Date.now();
  const tsHex = ms.toString(16).padStart(12, '0');
  const part1 = tsHex.slice(0, 8);
  const part2 = tsHex.slice(8, 12);
  // Set the version nibble to 7, then 3 random nibbles for the rest
  // of the third group.
  const part3 = '7' + randomHex(3);
  // Variant: top two bits are 10 (i.e. first nibble is 8/9/a/b).
  const variantNibble = HEX[(Math.random() * 4 + 8) | 0];
  const part4 = variantNibble + randomHex(3);
  const part5 = randomHex(12);
  return `${part1}-${part2}-${part3}-${part4}-${part5}`;
}
