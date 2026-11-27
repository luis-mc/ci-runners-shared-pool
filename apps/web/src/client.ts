// The site's only script: it puts the visitor's system first on the Download page (website R5.9). The page is complete without it.
import '@crsp/ui/ui.css';
import { putSystemFirst } from './lib/detect.ts';

putSystemFirst(document, navigator.userAgent);
