'use strict';

// The switch for the page's theme, which is on the login screen and on the
// pages about the map, where the appearance panel is not. It holds nothing
// of its own: a press changes the theme the record keeps, and whatever
// changes it from anywhere else, another tab or the panel, moves the switch.
// High contrast is for the panel alone, so while it is chosen no button is
// pressed.
(() => {
  const settings = window.mcmapSettings;
  const holders = [...document.querySelectorAll('[data-theme-switch]')];
  if (!settings || holders.length === 0) return;

  const ICONS = {
    system: '<rect x="2" y="3" width="12" height="8" rx="1.5"/><path d="M6 14h4M8 11v3"/>',
    light: '<circle cx="8" cy="8" r="3"/><path d="M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M3.4 12.6l1-1M11.6 4.4l1-1"/>',
    dark: '<path d="M13.5 9.5A5.5 5.5 0 1 1 6.5 2.5a4.5 4.5 0 0 0 7 7Z"/>',
  };
  const NAMES = { system: 'System', light: 'Light', dark: 'Dark' };

  const buttons = [];
  for (const holder of holders) {
    holder.setAttribute('role', 'group');
    holder.setAttribute('aria-label', 'Theme');
    for (const theme of Object.keys(NAMES)) {
      const button = document.createElement('button');
      button.type = 'button';
      button.dataset.theme = theme;
      button.setAttribute('aria-label', NAMES[theme]);
      button.title = NAMES[theme];
      button.innerHTML = `<svg viewBox="0 0 16 16" aria-hidden="true">${ICONS[theme]}</svg>`;
      button.addEventListener('click', () => settings.set('look', { ...settings.get('look'), theme }));
      holder.append(button);
      buttons.push(button);
    }
  }

  function show() {
    const chosen = settings.get('look').theme;
    for (const button of buttons) button.setAttribute('aria-pressed', String(button.dataset.theme === chosen));
  }
  show();
  document.addEventListener('mcmap:settings', show);
})();
