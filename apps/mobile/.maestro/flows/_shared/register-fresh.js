// Mint a unique account per Maestro run.
//
// Maestro's runScript executes Rhino-compatible JavaScript with no
// network access — keep this pure. We hash Date.now() + Math.random
// to 12 lowercase alphanumeric chars so the username passes the
// backend's `validate:"alphanum,min=3,max=32"` rule and stays under
// the email local-part length limit.

const seed = String(Date.now()) + String(Math.floor(Math.random() * 1e9));
const slug = seed
  .replace(/[^0-9a-z]/gi, '')
  .toLowerCase()
  .slice(-12)
  .padStart(12, '0');
output.username = 'm' + slug;
output.email = output.username + '@maestro.wakeup.test';
output.displayName = 'Maestro ' + slug;
output.password = 'Maestro!Test1234';
