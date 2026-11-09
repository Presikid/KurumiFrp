'use strict';

// The page and account actions keep their existing handlers. This file only
// manages the mobile navigation drawer and its keyboard focus.
(() => {
 const app = document.getElementById('app');
 const sidebar = document.getElementById('sidebar');
 const nav = document.getElementById('nav');
 const toggle = document.getElementById('menu-toggle');
 const close = document.getElementById('menu-close');
 const scrim = document.getElementById('nav-scrim');
 const content = document.getElementById('content');
 const mobile = window.matchMedia('(max-width: 760px)');

 function closeMenu(restoreFocus = false) {
  const wasOpen = app.classList.contains('menu-open');
  app.classList.remove('menu-open');
  document.body.classList.remove('navigation-open');
  toggle.setAttribute('aria-expanded', 'false');
  toggle.setAttribute('aria-label', '打开导航');
  if (wasOpen && restoreFocus && mobile.matches && !app.hidden) toggle.focus();
 }

 function openMenu() {
  if (!mobile.matches) return;
  app.classList.add('menu-open');
  document.body.classList.add('navigation-open');
  toggle.setAttribute('aria-expanded', 'true');
  toggle.setAttribute('aria-label', '关闭导航');
  (nav.querySelector('button.active') || nav.querySelector('button')).focus();
 }

 function syncCurrentPage() {
  nav.querySelectorAll('[data-page]').forEach(button => {
   if (button.classList.contains('active')) button.setAttribute('aria-current', 'page');
   else button.removeAttribute('aria-current');
  });
 }

 toggle.addEventListener('click', () => {
  if (app.classList.contains('menu-open')) closeMenu(true);
  else openMenu();
 });
 close.addEventListener('click', () => closeMenu(true));
 scrim.addEventListener('click', () => closeMenu(true));
 nav.addEventListener('click', event => {
  if (!event.target.closest('[data-page]') || !app.classList.contains('menu-open')) return;
  closeMenu();
  content.focus({preventScroll: true});
 });
 sidebar.addEventListener('click', event => {
  if (!app.classList.contains('menu-open')) return;
  if (event.target.closest('.brand')) {
   closeMenu();
   content.focus({preventScroll: true});
  } else if (event.target.closest('#logout, #quit-app')) closeMenu(true);
 });
 document.addEventListener('keydown', event => {
  if (!app.classList.contains('menu-open')) return;
  if (event.key === 'Escape') {
   event.preventDefault();
   closeMenu(true);
  } else if (event.key === 'Tab') {
   const items = [...sidebar.querySelectorAll('a[href], button:not([disabled])')]
    .filter(item => item.getClientRects().length);
   if (!items.length) return;
   const first = items[0], last = items[items.length - 1];
   if (event.shiftKey && (document.activeElement === first || !sidebar.contains(document.activeElement))) {
    event.preventDefault(); last.focus();
   } else if (!event.shiftKey && (document.activeElement === last || !sidebar.contains(document.activeElement))) {
    event.preventDefault(); first.focus();
   }
  }
 });
 mobile.addEventListener('change', () => closeMenu());
 new MutationObserver(() => {
  if (!app.hidden) return;
  closeMenu();
  document.querySelector('#login-form input[name="token"]').focus();
 }).observe(app, {attributes: true, attributeFilter: ['hidden']});
 new MutationObserver(syncCurrentPage).observe(nav, {subtree: true, attributes: true, attributeFilter: ['class']});
 syncCurrentPage();
})();
