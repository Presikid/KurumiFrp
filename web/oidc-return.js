'use strict';
// Replace the callback URL before returning so the authorization code and
// state are not retained in the current browser history entry.
const next = document.querySelector('meta[name="oidc-next"]')?.content;
if (next === '/' || next === '/?oidc=failed') {
 try {history.replaceState(null, '', '/');} catch (_) { /* location.replace still replaces this entry. */ }
 location.replace(next);
}
