import { createRoot } from 'react-dom/client';
import { StrictMode } from 'react';
import { App } from './App.tsx';
import './global.css';

const root = document.getElementById('root');
if (!root) throw new Error('missing root');
createRoot(root).render(<StrictMode><App /></StrictMode>);
