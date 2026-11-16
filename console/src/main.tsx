import '@fontsource-variable/geist';
import '@fontsource-variable/geist-mono';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { store } from './state/store';
import { applyTheme } from './state/theme';
import './styles.css';
import './landing.css';

// Set the theme before the first paint, and follow the system if the user chose that.
applyTheme(store.get().theme);
if (typeof matchMedia === 'function') {
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => store.syncSystemTheme());
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
