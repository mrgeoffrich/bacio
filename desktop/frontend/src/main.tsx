import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.jsx'
import { installGlobalErrorHandlers } from './errors'
import './styles/tokens.css'
import './styles/app.css'
import './styles/desktop.css'

installGlobalErrorHandlers()

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
