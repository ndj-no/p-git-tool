export namespace domain {
	
	export class AuthProfile {
	    id: string;
	    name: string;
	    provider: string;
	    username: string;
	    is_default: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AuthProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.username = source["username"];
	        this.is_default = source["is_default"];
	    }
	}
	export class Repo {
	    id: string;
	    name: string;
	    url: string;
	    auth_profile_id: string;
	    tags: string[];
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new Repo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.url = source["url"];
	        this.auth_profile_id = source["auth_profile_id"];
	        this.tags = source["tags"];
	        this.description = source["description"];
	    }
	}
	export class WorkspaceConfig {
	    default_root_path: string;
	    worker_count: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.default_root_path = source["default_root_path"];
	        this.worker_count = source["worker_count"];
	    }
	}

}

