// Optional development tool: Prettier standalone files live in .tools/prettier.
import fs from 'node:fs/promises';
import { format } from '../.tools/prettier/standalone.mjs';
import * as babel from '../.tools/prettier/plugins/babel.mjs';
import * as estree from '../.tools/prettier/plugins/estree.mjs';
import * as postcss from '../.tools/prettier/plugins/postcss.mjs';
import * as html from '../.tools/prettier/plugins/html.mjs';
for (const [name,parser] of [['app.js','babel'],['api.js','babel'],['priority.js','babel'],['gallery.js','babel'],['gallery.css','css'],['priority.css','css'],['style.css','css'],['index.html','html']]) {
 const path=new URL('../internal/web/assets/'+name,import.meta.url);
 await fs.writeFile(path,await format(await fs.readFile(path,'utf8'),{parser,plugins:[babel,estree,postcss,html],singleQuote:true,printWidth:100}));
}
