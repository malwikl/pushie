'use strict';
function element(tag='div'){
 const node={tagName:tag.toUpperCase(),children:[],listeners:{},attributes:{},value:'',hidden:false,open:false,
  append(...items){this.children.push(...items)},prepend(item){this.children.unshift(item)},replaceChildren(...items){this.children=items},
  setAttribute(name,value){this.attributes[name]=value},
  addEventListener(type,fn){(this.listeners[type] ||= []).push(fn)},
  async dispatch(type,event={}){for(const fn of this.listeners[type] || [])await fn(event)},
  querySelectorAll(selector){
   const matches=node=>selector==='input:checked'?node.tagName==='INPUT'&&node.checked:selector.startsWith('.')?node.className?.split(' ').includes(selector.slice(1)):false;
   return this.children.flatMap(child=>typeof child==='object'?[...(matches(child)?[child]:[]),...child.querySelectorAll(selector)]:[]);
  },
  querySelector(selector){return this.querySelectorAll(selector)[0] || null}
 };
 node.className='';
 node.classList={add(...names){node.className=[...new Set([...node.className.split(' ').filter(Boolean),...names])].join(' ')},remove(name){node.className=node.className.split(' ').filter(value=>value!==name).join(' ')}};
 return node;
}
module.exports={element};
