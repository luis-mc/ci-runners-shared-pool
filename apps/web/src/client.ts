// The site's only script: it puts the visitor's system first on the Download page (website R5.9) and runs the docs
// search (R5.19). Every page is complete without it.
import '@crsp/ui/ui.css';
import { putSystemFirst } from './lib/detect.ts';
import { attachDocSearch } from './lib/docsearch.ts';

putSystemFirst(document, navigator.userAgent);
attachDocSearch(document);
