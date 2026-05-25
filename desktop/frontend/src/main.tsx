import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import App from './App.jsx'
import { installGlobalErrorHandlers } from './errors'
import './styles/tokens.css'
import './styles/app.css'
import './styles/desktop.css'

installGlobalErrorHandlers()

// BACI-203: BrowserRouter on both surfaces. The `basename` resolves to
// `/ui` in web mode (mounted under /ui/ by `bacio web`) and the empty
// string in desktop mode (Wails serves the bundle at /). Both asset
// servers already implement SPA fallback (unknown non-asset paths under
// the root return index.html) — see internal/api/static.go for the
// `bacio web` path and desktop/main.go's AssetFileServerFS for Wails.
const basename = import.meta.env.BASE_URL.replace(/\/$/, '')

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <BrowserRouter basename={basename}>
      <App />
    </BrowserRouter>
  </React.StrictMode>,
)
