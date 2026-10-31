import type { ImageSourcePropType } from 'react-native';
import type { CharacterId } from '../shared/types';

export const characters: {id:CharacterId;name:string;description:string;source:ImageSourcePropType;colors:readonly [string,string];tint:string}[] = [
 {id:'lumi',name:'Люми',description:'За маленькие открытия',source:require('../assets/lumi.png'),colors:['#DCEAFF','#A9D6FF'],tint:'#E4EEFF'},
 {id:'spark',name:'Искра',description:'Добавит немного солнца',source:require('../assets/characters/spark.png'),colors:['#FFF4D6','#FFE394'],tint:'#FFF1C9'},
 {id:'nova',name:'Нова',description:'Всё получится в твоём темпе',source:require('../assets/characters/nova.png'),colors:['#EEE5FF','#D6BEFF'],tint:'#EEE4FF'},
];
export function getCharacter(id:CharacterId|null|undefined){return characters.find(character=>character.id===id)||characters[0];}
